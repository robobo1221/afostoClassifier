package resposnseclassifier

import (
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	psqr "robin.stik/server/psqr"

	database "robin.stik/server/database"
)

type Response struct {
	time int
	code int
}

type ResponseClassifier struct {
	connectionName       string
	maxPercentileMult    float32
	maxAbsoluteTime      int
	include4xx           bool
	currentResponse      Response
	currentScore         float64
	windowSize           int
	previousScores       []float64 // To store previous 5 scores
	previousMetricScores []float64
	CurrentPromMetrics   PromMetrics
}

type ResponseClassifiers struct {
	classifiers map[string]*ResponseClassifier // map of connectionName to ResponseClassifier
}

type classifyMiddleware struct {
	handler    http.HandlerFunc
	classifier *ResponseClassifier
}

type PromMetrics struct {
	// prometheus metrics
	ResponseTime prometheus.Counter
	ResponseCode prometheus.Histogram
	Score        prometheus.Gauge
}

func sanitizeMetricName(name string) string {
	// Replace '/' with '_'
	sanitized := strings.ReplaceAll(name, "/", "_")
	// Add more replacements if necessary
	return sanitized
}

func NewPromMetrics(connectionName string) *PromMetrics {
	sanitizedName := sanitizeMetricName(connectionName)

	metrics := &PromMetrics{
		ResponseTime: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: fmt.Sprintf("response_time_%s", sanitizedName),
				Help: "Response time of the request",
			},
		),
		ResponseCode: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Name:    fmt.Sprintf("response_code_%s", sanitizedName),
				Help:    "Response code of the request",
				Buckets: []float64{200, 300, 400, 500, 600},
			},
		),
		Score: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: fmt.Sprintf("score_%s", sanitizedName),
				Help: "Score of the request",
			},
		),
	}

	fmt.Printf("Registered PromMetrics for %s: %+v\n", sanitizedName, metrics)

	// Register the metrics with Prometheus
	if err := prometheus.Register(metrics.ResponseTime); err != nil {
		fmt.Printf("Error registering ResponseTime metric: %v\n", err)
	}
	if err := prometheus.Register(metrics.ResponseCode); err != nil {
		fmt.Printf("Error registering ResponseCode metric: %v\n", err)
	}
	if err := prometheus.Register(metrics.Score); err != nil {
		fmt.Printf("Error registering Score metric: %v\n", err)
	}

	return metrics
}

func NewResponseClassifier(connectionName string, maxPercentileMult float32, include4xx bool, windowSize int, maxAbsoluteTime int) *ResponseClassifier {
	return &ResponseClassifier{
		connectionName:     connectionName,
		maxPercentileMult:  maxPercentileMult,
		maxAbsoluteTime:    maxAbsoluteTime,
		include4xx:         include4xx,
		currentResponse:    Response{time: 0, code: 0},
		currentScore:       1.0,
		windowSize:         windowSize,
		previousScores:     make([]float64, 0, 5), // Initialize slice for previous scores
		CurrentPromMetrics: *NewPromMetrics(connectionName),
	}
}

// Smooths the current score based on the gaussian curve of the last 10 scores
func (rc *ResponseClassifier) applyLowPassFilter(newScore float64) float64 {
	// gaussian weights
	weights := []float64{0.1, 0.15, 0.2, 0.15, 0.1}

	// Add the new score to the previous scores
	rc.previousScores = append(rc.previousScores, newScore)

	// If the length of the previous scores is greater than 10, remove the first element
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

	psqr := psqr.NewPsqr(foundPerc)

	if foundPerc == 0 {
		return psqr
	}

	psqr.Count = count
	psqr.Q = q
	psqr.N = n
	psqr.Np = np
	psqr.Dn = dn

	return psqr
}

func (rc *ResponseClassifier) getPsqr(perc float64) (int, any, *psqr.Psqr) {
	id, previousPsqrId, foundPerc, count, q, n, np, dn := database.GetPsqrFromConnection(rc.connectionName, perc)

	psqr := psqr.NewPsqr(perc)

	if foundPerc == 0 {
		return -1, nil, psqr
	}

	psqr.Count = count
	psqr.Q = q
	psqr.N = n
	psqr.Np = np
	psqr.Dn = dn

	return id, previousPsqrId, psqr
}

func (rc *ResponseClassifier) Classify() float64 {
	defer rc.RecordMetrics()

	// classify response
	response := &rc.currentResponse
	if response.code >= 400 && rc.include4xx || response.code >= 500 {
		newScore := -2.0
		rc.currentScore = newScore

		return rc.currentScore
	}

	precentile := 0.95

	_, previousId, psqr := rc.getPsqr(precentile)

	if (psqr.Count+1)%rc.windowSize == 0 {
		database.SwapPsqr(rc.connectionName, precentile)

		// get new psqr and reset
		_, _, psqr = rc.getPsqr(precentile)
		psqr.Reset()
	}

	psqr.Add(float64(response.time))
	// update the psqr values in the database
	rc.RegisterData(psqr)

	p90 := psqr.Get()
	score := 1.0

	if previousId != nil {
		prevId := int(previousId.(int64))
		previousPsqr := rc.getPreviousPsqr(prevId)
		prevP90 := previousPsqr.Get()
		n := psqr.Count
		w2 := float64(n%rc.windowSize+1) / float64(rc.windowSize)
		w1 := 1.0 - w2
		p90 = w1*prevP90 + w2*p90
	}

	if previousId != nil || psqr.Count > 5 {
		upperLimit := math.Min(float64(rc.maxPercentileMult)*p90, float64(rc.maxAbsoluteTime))
		score = (upperLimit - float64(response.time)) / upperLimit
	}

	// Apply the low-pass filter to smooth the score
	smoothedScore := rc.applyLowPassFilter(score)

	rc.currentScore = smoothedScore

	return rc.currentScore
}

func (rc *ResponseClassifier) ResetPrevScores() {
	rc.previousMetricScores = []float64{}
}

func (rc *ResponseClassifier) RecordMetrics() {
	rc.previousMetricScores = append(rc.previousMetricScores, rc.currentScore)

	// Record the metrics to Prometheus

	// Update Prometheus metrics
	rc.CurrentPromMetrics.ResponseTime.Add(float64(rc.currentResponse.time))
	rc.CurrentPromMetrics.ResponseCode.Observe(float64(rc.currentResponse.code))

	rc.CurrentPromMetrics.Score.Set(average(rc.previousMetricScores))
}

func (rc *ResponseClassifier) registerPreviousData(id int, psqr *psqr.Psqr) {
	// Register previous data in database
	database.UpdatePsqr(
		id,
		psqr.Perc,
		psqr.Count,
		psqr.Q[0], psqr.Q[1], psqr.Q[2], psqr.Q[3], psqr.Q[4],
		psqr.N[0], psqr.N[1], psqr.N[2], psqr.N[3], psqr.N[4],
		psqr.Np[0], psqr.Np[1], psqr.Np[2], psqr.Np[3], psqr.Np[4],
		psqr.Dn[0], psqr.Dn[1], psqr.Dn[2], psqr.Dn[3], psqr.Dn[4],
	)
}

func (rc *ResponseClassifier) RegisterData(psqr *psqr.Psqr) {
	// register data in database
	database.InsertConnectionWithPsqr(
		rc.connectionName,
		psqr.Perc,
		psqr.Count,
		psqr.Q[0], psqr.Q[1], psqr.Q[2], psqr.Q[3], psqr.Q[4],
		psqr.N[0], psqr.N[1], psqr.N[2], psqr.N[3], psqr.N[4],
		psqr.Np[0], psqr.Np[1], psqr.Np[2], psqr.Np[3], psqr.Np[4],
		psqr.Dn[0], psqr.Dn[1], psqr.Dn[2], psqr.Dn[3], psqr.Dn[4],
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

func NewResponseClassifiers() ResponseClassifiers {
	return ResponseClassifiers{
		classifiers: make(map[string]*ResponseClassifier),
	}
}

func NewResponseClassifiersWithClassifiers(connections []string) {
	rcs := NewResponseClassifiers()
	for _, connection := range connections {
		rcs.Dispatch(connection)
	}
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
