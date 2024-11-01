package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/robobo1221/afostoClassifier/classifier"
	"github.com/robobo1221/afostoClassifier/database"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
)

func sendRequest(ctx context.Context, client *http.Client) {
	urls := []string{
		"https://afosto.com",
		"https://google.com",
		"https://facebook.com",
		"https://twitter.com",
		"https://instagram.com",
		"https://linkedin.com",
		"https://youtube.com",
		"https://reddit.com",
		"https://tiktok.com",
		"https://netflix.com",
	}

	for {
		for _, url := range urls {
			go func(url string) {
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
				resp, err := client.Do(req)
				if err != nil {
					fmt.Printf("Error fetching %s: %v\n", url, err)
					return
				}
				defer resp.Body.Close()
			}(url)
		}
		time.Sleep(1 * time.Second) // Rate limiting the requests
	}
}

func setupCollector(ctx context.Context) (*sdktrace.TracerProvider, *metric.MeterProvider, error) {
	const endpoint = "localhost:4317"

	traceExp, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(endpoint), otlptracegrpc.WithInsecure())
	if err != nil {
		return nil, nil, err
	}

	metricExp, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithEndpoint(endpoint), otlpmetricgrpc.WithInsecure())
	if err != nil {
		return nil, nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("testing-service"),
		)),
	)

	mp := metric.NewMeterProvider(
		metric.WithReader(metric.NewPeriodicReader(metricExp, metric.WithInterval(5*time.Second))),
		metric.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("testing-service"),
		)),
	)

	return tp, mp, nil
}

func main() {
	ctx := context.Background()
	tp, mp, err := setupCollector(ctx)
	if err != nil {
		fmt.Println("Error setting up collector:", err)
		return
	}

	// Set the global TracerProvider and MeterProvider
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)

	// Initialize database
	database.InitSqlite()
	database.Migrate()

	// Create a new client with the custom RoundTripper
	client := &http.Client{
		Transport: classifier.NewClassifierRoundTripper(classifier.ResponseClassifiersInstance),
	}

	http.HandleFunc("/send", func(w http.ResponseWriter, r *http.Request) {
		go sendRequest(ctx, client)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Printf("Server is running on port %s\n", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		fmt.Println("Error starting server:", err)
	}

}

/*
func graphDatas(w http.ResponseWriter, r *http.Request) {
	plotActualvsApproximated(dataReal, data, "data.png", "Data")
	plotErrorRates(errorRates, "error.png", "Error")
	plotScores(scores, "scores.png", "Scores")

	fmt.Fprintln(w, "Graphs created")
}

func plotErrorRates(errorRates []float64, name string, title string) {
	// Create a plot
	p := plot.New()
	p.Title.Text = "Error rates " + title
	p.X.Label.Text = "Observation #"
	p.Y.Label.Text = "Error rate"
	p.Y.Max = 1.0
	p.Y.Min = 0.0

	pts := make(plotter.XYs, len(errorRates))
	for i, errorRate := range errorRates {
		pts[i].X = float64(i)
		pts[i].Y = errorRate
	}

	// Create a line plot
	line, err := plotter.NewLine(pts)
	if err != nil {
		panic(err)
	}
	p.Add(line)

	// Save the plot to a PNG file
	if err := p.Save(6*vg.Inch, 4*vg.Inch, "./images/"+name); err != nil {
		panic(err)
	}
}

func plotActualvsApproximated(actualValues []float64, approximated []float64, name string, title string) {
	// Create a plot
	p := plot.New()
	p.Title.Text = "Actual vs Approximated " + title
	p.X.Label.Text = "Observation #"
	p.Y.Label.Text = "Value"

	// Determine Y-axis range
	var maxY, minY float64
	if len(actualValues) > 0 {
		maxY = actualValues[0]
		minY = actualValues[0]
		for _, v := range actualValues {
			if v > maxY {
				maxY = v
			}
			if v < minY {
				minY = v
			}
		}
	}
	if len(approximated) > 0 {
		for _, v := range approximated {
			if v > maxY {
				maxY = v
			}
			if v < minY {
				minY = v
			}
		}
	}
	p.Y.Max = maxY * 1.1
	p.Y.Min = minY * 0.9

	// Actual values line
	actualPts := make(plotter.XYs, len(actualValues))
	for i, val := range actualValues {
		actualPts[i].X = float64(i)
		actualPts[i].Y = val
	}
	actualLine, err := plotter.NewLine(actualPts)
	if err != nil {
		panic(err)
	}
	actualLine.Color = color.RGBA{R: 0, G: 0, B: 255, A: 255} // Blue
	p.Add(actualLine)
	p.Legend.Add("Actual", actualLine)

	// Approximated values line
	approxPts := make(plotter.XYs, len(approximated))
	for i, val := range approximated {
		approxPts[i].X = float64(i)
		approxPts[i].Y = val
	}
	approxLine, err := plotter.NewLine(approxPts)
	if err != nil {
		panic(err)
	}
	approxLine.Color = color.RGBA{R: 255, G: 0, B: 0, A: 255} // Red
	p.Add(approxLine)
	p.Legend.Add("Approximated", approxLine)

	// Save the plot to a PNG file
	if err := p.Save(6*vg.Inch, 4*vg.Inch, "./images/"+name); err != nil {
		panic(err)
	}
}

func plotSegment(p *plot.Plot, segment plotter.XYs, above bool) {
	line, err := plotter.NewLine(segment)
	if err != nil {
		panic(err)
	}
	if above {
		line.Color = color.RGBA{R: 0, G: 255, B: 0, A: 255} // Green for above threshold
	} else {
		line.Color = color.RGBA{R: 255, G: 0, B: 0, A: 255} // Red for below threshold
	}
	p.Add(line)
}

func plotScores(scores []float64, name string, title string) {
	// Create a plot
	p := plot.New()
	p.Title.Text = "Connection Scores " + title
	p.X.Label.Text = "Connection #"
	p.Y.Label.Text = "Score"
	p.Y.Max = 1.0
	p.Y.Min = -2.0

	var currentSegment plotter.XYs
	var prevAbove bool
	var firstSegment bool = true
	var prevPoint plotter.XY // To keep track of the previous point for edge connection

	for i, score := range scores {
		currentAbove := score >= 0.0

		// If switching segments, plot the previous segment and start a new one
		if !firstSegment && currentAbove != prevAbove {
			// Plot the previous segment
			plotSegment(p, currentSegment, prevAbove)

			// Plot the connecting line (edge) to connect segments
			edgeSegment := plotter.XYs{
				{X: prevPoint.X, Y: prevPoint.Y},
				{X: float64(i), Y: score},
			}
			plotSegment(p, edgeSegment, currentAbove)

			currentSegment = plotter.XYs{}
		}

		// Add the current point to the segment
		currentSegment = append(currentSegment, plotter.XY{X: float64(i), Y: score})
		prevPoint = plotter.XY{X: float64(i), Y: score}
		prevAbove = currentAbove
		firstSegment = false
	}

	// Plot the last segment
	if len(currentSegment) > 0 {
		plotSegment(p, currentSegment, prevAbove)
	}

	// Save the plot to a PNG file
	if err := p.Save(6*vg.Inch, 4*vg.Inch, "./images/"+name); err != nil {
		panic(err)
	}
}

func calculatePercentile(data []float64, percentile float64) float64 {
	sortedData := make([]float64, len(data))
	copy(sortedData, data)
	sort.Float64s(sortedData)

	n := len(sortedData)
	if n == 0 {
		return 0
	}
	index := int(math.Ceil(float64(n)*percentile)) - 1
	if index < 0 {
		index = 0
	}
	if index >= n {
		index = n - 1
	}

	return sortedData[index]
}

func handleData(w http.ResponseWriter, classifier *classifier.ResponseClassifier, resp *http.Response, respTime int64) {
	_, _, perc, count, q, n, np, dn := database.GetPsqrFromConnection(classifier.GetConnectionName(), 0.95)

	latestResponse := classifier.GetResponse()
	latestResponseCode := latestResponse.GetCode()

	if !(latestResponseCode > 400) {
		nRequest++
		if nRequest%classifier.GetWindowSize() == 0 {
			previousPercentile = calculatePercentile(responses, 0.95)
			responses = []float64{} // Reset the responses to simulate a new window
		}

		responses = append(responses, float64(respTime))
	}

	realPerc := calculatePercentile(responses, 0.95)

	if previousPercentile != -1 {
		w2 := float64(nRequest%classifier.GetWindowSize()+1) / float64(classifier.GetWindowSize())
		w1 := 1.0 - w2

		realPerc = w2*realPerc + w1*previousPercentile
	}

	newPsqr := psqr.NewPsqr(0.95)
	newPsqr.Count = count
	newPsqr.Q = q
	newPsqr.N = n
	newPsqr.Np = np
	newPsqr.Dn = dn

	data = append(data, newPsqr.Get())
	dataReal = append(dataReal, realPerc)

	errorRates = append(errorRates, math.Abs(realPerc-newPsqr.Get()))
	scores = append(scores, classifier.GetScore())

	fmt.Fprintln(w, "Status:", resp.StatusCode)
	fmt.Fprintln(w, "Response Time:", respTime)
	fmt.Fprintln(w, "Classifier:", classifier.GetConnectionName())
	fmt.Fprintln(w, "Score:", classifier.GetScore())
	fmt.Fprintln(w, "Perc:", perc)
	fmt.Fprintln(w, "Count:", count)
	fmt.Fprintln(w, "Q:", q)
	fmt.Fprintln(w, "N:", n)
	fmt.Fprintln(w, "Np:", np)
	fmt.Fprintln(w, "Dn:", dn)
}
*/
