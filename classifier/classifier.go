package resposnseclassifier

import (
	"context"
	"fmt"
	"math"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"robin.stik/server/database"
	psqr "robin.stik/server/psqr"
)

type Response struct {
	time int
	code int
}

type ResponseClassifier struct {
	connectionName    string
	maxPercentileMult float32
	maxAbsoluteTime   int
	include4xx        bool
	currentResponse   Response
	currentScore      float64
	windowSize        int
	previousScores    []float64 // To store previous 5 scores
}

type ResponseClassifiers struct {
	classifiers        map[string]*ResponseClassifier // map of connectionName to ResponseClassifier
	CurrentOtelMetrics *OtelMetrics
}

type ClassifyMiddleware struct {
	handler    http.HandlerFunc
	classifier *ResponseClassifier
}

type OtelMetrics struct {
	ResponseTime metric.Float64Histogram
	Score        metric.Float64Histogram
}

func NewOtelMetrics(meter metric.Meter) *OtelMetrics {
	responseTime, err := meter.Float64Histogram(
		"http_response_time",
		metric.WithDescription("Response time of the request in milliseconds"),
	)
	if err != nil {
		fmt.Printf("Error creating ResponseTime histogram: %v\n", err)
	}

	score, err := meter.Float64Histogram(
		"http_request_score",
		metric.WithDescription("Score of the request"),
		metric.WithExplicitBucketBoundaries(0.01, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0),
	)
	if err != nil {
		fmt.Printf("Error creating Score histogram: %v\n", err)
	}

	fmt.Println("Registered OpenTelemetry Metrics.")

	return &OtelMetrics{
		ResponseTime: responseTime,
		Score:        score,
	}
}

func NewResponseClassifier(connectionName string, maxPercentileMult float32, include4xx bool, windowSize int, maxAbsoluteTime int) *ResponseClassifier {
	return &ResponseClassifier{
		connectionName:    connectionName,
		maxPercentileMult: maxPercentileMult,
		maxAbsoluteTime:   maxAbsoluteTime,
		include4xx:        include4xx,
		currentResponse:   Response{time: 0, code: 0},
		currentScore:      1.0,
		windowSize:        windowSize,
		previousScores:    make([]float64, 0, 5), // Initialize slice for previous scores
	}
}

// Smooths the current score based on the gaussian curve of the last 5 scores
func (rc *ResponseClassifier) applyLowPassFilter(newScore float64) float64 {
	// gaussian weights
	weights := []float64{0.1, 0.15, 0.2, 0.15, 0.1}

	// Add the new score to the previous scores
	rc.previousScores = append(rc.previousScores, newScore)

	// If the length of the previous scores is greater than 5, remove the first element
	if len(rc.previousScores) > 5 {
		rc.previousScores = rc.previousScores[1:]
	}

	// Initialize the smoothed score
	smoothedScore := 0.0
	totalWeight := 0.0

	// Apply the gaussian weights to the previous scores
	for i, score := range rc.previousScores {
		smoothedScore += score * weights[i]
		totalWeight += weights[i]
	}

	return smoothedScore / totalWeight
}

func (rc *ResponseClassifier) getPreviousPsqr(id int) *psqr.Psqr {
	_, _, foundPerc, count, q, n, np, dn := database.GetPsqr(id)

	psqrObj := psqr.NewPsqr(foundPerc)

	if foundPerc == 0 {
		return psqrObj
	}

	psqrObj.Count = count
	psqrObj.Q = q
	psqrObj.N = n
	psqrObj.Np = np
	psqrObj.Dn = dn

	return psqrObj
}

func (rc *ResponseClassifier) getPsqr(perc float64) (int, any, *psqr.Psqr) {
	id, previousPsqrId, foundPerc, count, q, n, np, dn := database.GetPsqrFromConnection(rc.connectionName, perc)

	psqrObj := psqr.NewPsqr(perc)

	if foundPerc == 0 {
		return -1, nil, psqrObj
	}

	psqrObj.Count = count
	psqrObj.Q = q
	psqrObj.N = n
	psqrObj.Np = np
	psqrObj.Dn = dn

	return id, previousPsqrId, psqrObj
}

func (rc *ResponseClassifier) Classify() float64 {
	// classify response
	response := &rc.currentResponse
	if (response.code >= 400 && rc.include4xx) || response.code >= 500 {
		newScore := 0.0
		rc.currentScore = newScore
		return rc.currentScore
	}

	percentile := 0.95

	_, previousId, psqrObj := rc.getPsqr(percentile)

	p90 := psqrObj.Get()
	score := 1.0

	if previousId != nil {
		prevId := int(previousId.(int64))
		previousPsqr := rc.getPreviousPsqr(prevId)
		prevP90 := previousPsqr.Get()
		n := psqrObj.Count
		w2 := float64(n%rc.windowSize+1) / float64(rc.windowSize)
		w1 := 1.0 - w2
		p90 = w1*prevP90 + w2*p90
	}

	if previousId != nil || psqrObj.Count > 5 {
		upperLimit := math.Min(float64(rc.maxPercentileMult)*p90, float64(rc.maxAbsoluteTime))
		score = (upperLimit-float64(response.time))/math.Max(upperLimit, float64(response.time))*0.5 + 0.5 // Score between 0 and 1
	}

	// Apply the low-pass filter to smooth the score
	smoothedScore := rc.applyLowPassFilter(score)
	rc.currentScore = smoothedScore

	n := psqrObj.Count + 1

	if n%rc.windowSize == 0 {
		database.SwapPsqr(rc.connectionName, percentile)

		// reset the psqr values
		psqrObj.Reset()
	}

	psqrObj.Add(float64(response.time))
	// update the psqr values in the database
	rc.RegisterData(psqrObj)

	return rc.currentScore
}

func (rcs *ResponseClassifiers) RecordMetrics(ctx context.Context, rc *ResponseClassifier) {
	attrs := []attribute.KeyValue{
		attribute.String("connection_name", rc.connectionName),
	}

	// Record metrics using OpenTelemetry instruments
	rcs.CurrentOtelMetrics.ResponseTime.Record(ctx, float64(rc.currentResponse.time), metric.WithAttributes(attrs...))
	rcs.CurrentOtelMetrics.Score.Record(ctx, rc.currentScore, metric.WithAttributes(attrs...))
}

func (rc *ResponseClassifier) registerPreviousData(id int, psqrObj *psqr.Psqr) {
	// Register previous data in database
	database.UpdatePsqr(
		id,
		psqrObj.Perc,
		psqrObj.Count,
		psqrObj.Q[0], psqrObj.Q[1], psqrObj.Q[2], psqrObj.Q[3], psqrObj.Q[4],
		psqrObj.N[0], psqrObj.N[1], psqrObj.N[2], psqrObj.N[3], psqrObj.N[4],
		psqrObj.Np[0], psqrObj.Np[1], psqrObj.Np[2], psqrObj.Np[3], psqrObj.Np[4],
		psqrObj.Dn[0], psqrObj.Dn[1], psqrObj.Dn[2], psqrObj.Dn[3], psqrObj.Dn[4],
	)
}

func (rc *ResponseClassifier) RegisterData(psqrObj *psqr.Psqr) {
	// register data in database
	database.InsertConnectionWithPsqr(
		rc.connectionName,
		psqrObj.Perc,
		psqrObj.Count,
		psqrObj.Q[0], psqrObj.Q[1], psqrObj.Q[2], psqrObj.Q[3], psqrObj.Q[4],
		psqrObj.N[0], psqrObj.N[1], psqrObj.N[2], psqrObj.N[3], psqrObj.N[4],
		psqrObj.Np[0], psqrObj.Np[1], psqrObj.Np[2], psqrObj.Np[3], psqrObj.Np[4],
		psqrObj.Dn[0], psqrObj.Dn[1], psqrObj.Dn[2], psqrObj.Dn[3], psqrObj.Dn[4],
	)
}

func (rc *ResponseClassifier) GetConnectionName() string {
	return rc.connectionName
}

func (rc *ResponseClassifier) SetResponse(time int, code int) {
	rc.currentResponse = NewResponse(time, code)
}

func (rc *ResponseClassifier) GetResponse() Response {
	return rc.currentResponse
}

func (rc *ResponseClassifier) GetScore() float64 {
	return rc.currentScore
}

func (rc *ResponseClassifier) GetWindowSize() int {
	return rc.windowSize
}

func NewResponseClassifiers(meter metric.Meter) *ResponseClassifiers {
	return &ResponseClassifiers{
		classifiers:        make(map[string]*ResponseClassifier),
		CurrentOtelMetrics: NewOtelMetrics(meter),
	}
}

func NewResponseClassifiersWithClassifiers(connections []string, meter metric.Meter) *ResponseClassifiers {
	rcs := NewResponseClassifiers(meter)
	for _, connection := range connections {
		rcs.Dispatch(connection)
	}
	return rcs
}

func (rcs *ResponseClassifiers) Dispatch(connection string) *ResponseClassifier {
	// check if connection already exists
	if classifier, ok := rcs.classifiers[connection]; ok {
		return classifier
	}

	newClassifier := NewResponseClassifier(connection, 1.5, false, 1000, 1000)
	rcs.classifiers[connection] = newClassifier

	return newClassifier
}

func (rcs *ResponseClassifiers) DispatchWithParams(connection string, maxPercentileMult float32, include4xx bool, windowSize int, maxAbsoluteTime int) *ResponseClassifier {
	// Check if connection already exists
	if classifier, ok := rcs.classifiers[connection]; ok {
		return classifier // Return the pointer to the value
	}

	newClassifier := NewResponseClassifier(connection, maxPercentileMult, include4xx, windowSize, maxAbsoluteTime)

	rcs.classifiers[connection] = newClassifier

	return newClassifier
}

func (rcs *ResponseClassifiers) DispatchWithParamsAndClassify(ctx context.Context, connection string, maxPercentileMult float32, include4xx bool, windowSize int, maxAbsoluteTime int, respTime int, code int) *ResponseClassifier {
	// Check if connection already exists
	if classifier, ok := rcs.classifiers[connection]; ok {
		classifier.SetResponse(respTime, code)
		classifier.Classify()
		rcs.RecordMetrics(ctx, classifier)
		return classifier // Return the pointer to the value
	}

	newClassifier := NewResponseClassifier(connection, maxPercentileMult, include4xx, windowSize, maxAbsoluteTime)
	newClassifier.SetResponse(respTime, code)
	newClassifier.Classify()
	rcs.RecordMetrics(ctx, newClassifier)

	rcs.classifiers[connection] = newClassifier

	return newClassifier
}

func (rcs *ResponseClassifiers) DispatchWithClassifier(
	classifier *ResponseClassifier,
) *ResponseClassifier {
	// check if connection already exists
	if _, ok := rcs.classifiers[classifier.connectionName]; ok {
		classifier := rcs.classifiers[classifier.connectionName]
		return classifier
	}

	rcs.classifiers[classifier.connectionName] = classifier

	return classifier
}

func (rcs *ResponseClassifiers) Get(connection string) *ResponseClassifier {
	if classifier, ok := rcs.classifiers[connection]; ok {
		return classifier
	}
	return nil
}

func (rcs ResponseClassifiers) GetClassifierKeys() []string {
	keys := make([]string, 0, len(rcs.classifiers))
	for k := range rcs.classifiers {
		keys = append(keys, k)
	}
	return keys
}

func minSlice(slice []float64) float64 {
	if len(slice) == 0 {
		return math.Inf(1) // Return positive infinity if the slice is empty
	}
	min := slice[0]
	for _, value := range slice {
		if value < min {
			min = value
		}
	}
	return min
}

func average(slice []float64) float64 {
	if len(slice) == 0 {
		return 0
	}
	sum := 0.0
	for _, value := range slice {
		sum += value
	}
	return sum / float64(len(slice))
}

func NewResponse(time int, code int) Response {
	return Response{
		time: time,
		code: code,
	}
}

func (r *Response) GetTime() int {
	return r.time
}

func (r *Response) GetCode() int {
	return r.code
}
