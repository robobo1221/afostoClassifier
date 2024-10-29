package classifier

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/robobo1221/afostoClassifier/database"
	psqr "github.com/robobo1221/afostoClassifier/psqr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
)

var (
	ResponseClassifiersInstance *ResponseClassifiers = NewResponseClassifiers()
)

type Response struct {
	time int
	code int
}

type ResponseClassifier struct {
	mu                sync.Mutex
	connectionName    string
	maxPercentileMult float32
	maxAbsoluteTime   int
	include4xx        bool
	currentResponse   Response
	currentScore      float64
	windowSize        int
	lastFiveScores    []float64
}

type ResponseClassifiers struct {
	classifiers        map[string]*ResponseClassifier // Map of connectionName to ResponseClassifier
	CurrentOtelMetrics *OtelMetrics
}

type OtelMetrics struct {
	ResponseTime  metric.Float64Histogram
	TotalRequests metric.Int64Counter
	Score         metric.Float64Histogram
}

func NewOtelMetrics() *OtelMetrics {
	meter := otel.GetMeterProvider().Meter("classifier-" + filepath.Base(os.Args[0]))

	responseTime, err := meter.Float64Histogram(
		"http_response_time",
		metric.WithDescription("Response time of the request in milliseconds"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		fmt.Printf("Error creating ResponseTime histogram: %v\n", err)
	}

	totalRequests, err := meter.Int64Counter(
		"http_total_requests",
		metric.WithDescription("Total number of requests"),
	)
	if err != nil {
		fmt.Printf("Error creating TotalRequests counter: %v\n", err)
	}

	score, err := meter.Float64Histogram(
		"http_request_score",
		metric.WithDescription("Score of the request"),
		metric.WithUnit("score"),
		metric.WithExplicitBucketBoundaries(0.01, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0),
	)
	if err != nil {
		fmt.Printf("Error creating Score histogram: %v\n", err)
	}

	fmt.Println("Registered OpenTelemetry Metrics.")

	return &OtelMetrics{
		ResponseTime:  responseTime,
		TotalRequests: totalRequests,
		Score:         score,
	}
}

func NewResponseClassifier(connectionName string, maxPercentileMult float32, include4xx bool, windowSize int, maxAbsoluteTime int) *ResponseClassifier {
	if maxAbsoluteTime < 0 {
		maxAbsoluteTime = 1e10
	}

	return &ResponseClassifier{
		connectionName:    connectionName,
		maxPercentileMult: maxPercentileMult,
		maxAbsoluteTime:   maxAbsoluteTime,
		include4xx:        include4xx,
		currentResponse:   Response{time: 0, code: 0},
		currentScore:      1.0,
		windowSize:        windowSize,
		lastFiveScores:    make([]float64, 5),
	}
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

func (rc *ResponseClassifier) applyLowPassFilter(score float64) float64 {
	rc.lastFiveScores = append(rc.lastFiveScores, score)
	if len(rc.lastFiveScores) > 5 {
		rc.lastFiveScores = rc.lastFiveScores[1:]
	}

	// Calculate the average of the last five scores
	average := 0.0
	for _, s := range rc.lastFiveScores {
		average += s
	}
	average /= float64(len(rc.lastFiveScores))

	return average
}

func (rc *ResponseClassifier) Classify(ctx context.Context) float64 {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	_, span := otel.GetTracerProvider().Tracer("connectionClassifier").Start(ctx, "Classify")
	defer span.End()

	// Classify response
	response := &rc.currentResponse
	if (response.code >= 400 && rc.include4xx) || response.code >= 500 {
		newScore := 0.0
		rc.currentScore = newScore

		// Error
		span.RecordError(fmt.Errorf("Error response code: %d", response.code))
		span.SetStatus(codes.Error, fmt.Sprintf("Error response code: %d", response.code))

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

	score = rc.applyLowPassFilter(score)

	if score < 0.5 {
		span.RecordError(fmt.Errorf("Response time too high: %d", response.time))
		span.SetStatus(codes.Error, fmt.Sprintf("Response time too high: %d", response.time))
	}

	// Apply the low-pass filter to smooth the score
	//smoothedScore := rc.applyLowPassFilter(score)
	rc.currentScore = score

	n := psqrObj.Count + 1

	if n%rc.windowSize == 0 {
		database.SwapPsqr(rc.connectionName, percentile)

		// Reset the psqr values
		psqrObj.Reset()
	}

	// Ensure the response is successful before adding the response time to the psqr object.
	if response.code < 400 {
		psqrObj.Add(float64(response.time))
		// Update the psqr values in the database
		rc.RegisterData(psqrObj)
	}

	return rc.currentScore
}

func (rcs *ResponseClassifiers) RecordMetrics(ctx context.Context, rc *ResponseClassifier) {
	tracer := otel.GetTracerProvider().Tracer("connectionClassifier")
	ctx, span := tracer.Start(ctx, "RecordMetrics")
	defer span.End()

	attrs := []attribute.KeyValue{
		attribute.String("connection_name", rc.connectionName),
	}

	attrsForCount := append(attrs, attribute.String("status_code", fmt.Sprintf("%d", rc.currentResponse.code)))

	// Record metrics
	rcs.CurrentOtelMetrics.ResponseTime.Record(ctx, float64(rc.currentResponse.time), metric.WithAttributes(attrs...))
	rcs.CurrentOtelMetrics.TotalRequests.Add(ctx, 60, metric.WithAttributes(attrsForCount...))
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
	// Register data in database
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
	rc.mu.Lock()
	defer rc.mu.Unlock()

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

func NewResponseClassifiers() *ResponseClassifiers {
	return &ResponseClassifiers{
		classifiers:        make(map[string]*ResponseClassifier),
		CurrentOtelMetrics: NewOtelMetrics(),
	}
}

func (rcs *ResponseClassifiers) DispatchWithParamsAndClassify(ctx context.Context, connection string, maxPercentileMult float32, include4xx bool, windowSize int, maxAbsoluteTime int, respTime int, code int) *ResponseClassifier {
	tracer := otel.GetTracerProvider().Tracer("connectionClassifier")
	ctx, span := tracer.Start(ctx, "DispatchWithParamsAndClassify")
	defer span.End()

	// Check if connection already exists
	if classifier, ok := rcs.classifiers[connection]; ok {
		classifier.SetResponse(respTime, code)
		classifier.Classify(ctx)
		rcs.RecordMetrics(ctx, classifier)
		return classifier // Return the pointer to the value
	}

	newClassifier := NewResponseClassifier(connection, maxPercentileMult, include4xx, windowSize, maxAbsoluteTime)
	newClassifier.SetResponse(respTime, code)
	newClassifier.Classify(ctx)
	rcs.RecordMetrics(ctx, newClassifier)

	rcs.classifiers[connection] = newClassifier

	return newClassifier
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

// Round tripper
type ClassifierRoundTripper struct {
	transport   http.RoundTripper
	classifiers *ResponseClassifiers
}

func (t *ClassifierRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Get the tracer
	tracer := otel.GetTracerProvider().Tracer("connectionClassifier")
	ctx, span := tracer.Start(req.Context(), "HTTP "+req.Method+" "+req.URL.String())
	defer span.End()

	// Start measuring response time
	timeStart := time.Now()
	resp, err := t.transport.RoundTrip(req)
	respTime := time.Since(timeStart).Milliseconds()

	// Handle errors
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}
	defer resp.Body.Close()

	span.SetStatus(codes.Ok, "Request successful")

	// Dispatch the classifier in a goroutine
	go t.classifiers.DispatchWithParamsAndClassify(
		ctx,
		req.URL.Host,
		1.0,
		true,
		1000,
		-1,
		int(respTime),
		resp.StatusCode,
	)

	fmt.Printf("classified %s %d %d\n", req.URL.Host, respTime, resp.StatusCode)

	return resp, nil
}

func NewClassifierRoundTripper(classifiers *ResponseClassifiers) http.RoundTripper {
	return &ClassifierRoundTripper{
		transport:   http.DefaultTransport,
		classifiers: classifiers,
	}
}
