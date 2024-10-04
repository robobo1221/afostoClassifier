package main

import (
	"fmt"
	"image/color"
	"math"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
	classifier "robin.stik/server/classifier"
	database "robin.stik/server/database"
	psqr "robin.stik/server/psqr"
)

var (
	classifiers = classifier.NewResponseClassifiers()

	// This is to plot the data
	data                       = []float64{}
	dataReal                   = []float64{}
	errorRates                 = []float64{}
	nRequest                   = 0
	responses                  = []float64{}
	scores                     = []float64{}
	previousPercentile float64 = -1.
)

func calculatePercentile(data []float64, percentile float64) float64 {
	sortedData := make([]float64, len(data))
	copy(sortedData, data)
	sort.Float64s(sortedData)

	n := len(sortedData)
	index := int(math.Ceil(float64(n) * percentile))

	if index == n {
		return sortedData[n-1]
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
			responses = []float64{} // reset the responses to simulate a new window
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
	fmt.Fprintln(w, "Resposnse Time:", respTime)
	fmt.Fprintln(w, "classifier:", classifier.GetConnectionName())
	fmt.Fprintln(w, "Score:", classifier.GetScore())
	fmt.Fprintln(w, "Perc:", perc)
	fmt.Fprintln(w, "Count:", count)
	fmt.Fprintln(w, "Q:", q)
	fmt.Fprintln(w, "N:", n)
	fmt.Fprintln(w, "Np:", np)
	fmt.Fprintln(w, "Dn:", dn)
}

func sendRequest(w http.ResponseWriter, r *http.Request) {
	timeStart := time.Now()
	resp, err := http.Get("https://bol.com/")

	respTime := time.Since(timeStart).Milliseconds()

	if err != nil {
		fmt.Println("Error:", err)
		fmt.Println("Response Time:", respTime)
	}
	defer resp.Body.Close()

	classifier := classifiers.DispatchWithParams("bol/index", 1.5, true, 1000, 1000)

	classifier.SetResponse(int(respTime), resp.StatusCode)
	classifier.Classify()

	handleData(w, classifier, resp, respTime)
}

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

	var currentSegment plotter.XYs

	for i, errorRate := range errorRates {
		currentSegment = append(currentSegment, plotter.XY{X: float64(i), Y: errorRate})
	}

	// Create a scatter plot
	scatter, err := plotter.NewLine(currentSegment)
	if err != nil {
		panic(err)
	}
	p.Add(scatter)

	// Save the plot to a PNG file
	if err := p.Save(6*3*vg.Inch, 4*3*vg.Inch, "./images/"+name); err != nil {
		panic(err)
	}
}

func plotActualvsApproximated(actualValues []float64, approximated []float64, name string, title string) {
	// Create a plot
	p := plot.New()
	p.Title.Text = "Actual vs Approximated " + title
	p.X.Label.Text = "Observation #"
	p.Y.Label.Text = "Value"

	// scale the y-axis to the actual values
	p.Y.Max = 1000.0
	p.Y.Min = 0.0

	var actualSegment plotter.XYs

	for i, actualValue := range actualValues {
		actualSegment = append(actualSegment, plotter.XY{X: float64(i), Y: actualValue})
	}

	// Create a scatter plot
	scatterActual, err := plotter.NewLine(actualSegment)
	if err != nil {
		panic(err)
	}
	scatterActual.Color = color.RGBA{R: 0, G: 0, B: 255, A: 255}
	p.Add(scatterActual)

	// plot the approximated values
	var approximatedSegment plotter.XYs

	for i, approximatedValue := range approximated {
		approximatedSegment = append(approximatedSegment, plotter.XY{X: float64(i), Y: approximatedValue})
	}

	// Create a scatter plot
	scatterApproximated, err := plotter.NewLine(approximatedSegment)
	if err != nil {
		panic(err)
	}

	scatterApproximated.Color = color.RGBA{R: 255, G: 0, B: 0, A: 255}
	p.Add(scatterApproximated)

	// Save the plot to a PNG file
	if err := p.Save(6*3*vg.Inch, 4*3*vg.Inch, "./images/"+name); err != nil {
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
	if err := p.Save(6*3*vg.Inch, 4*3*vg.Inch, "./images/"+name); err != nil {
		panic(err)
	}
}

func promWrapper(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r)

		// Reset the scores
		classifierKeys := classifiers.GetClassifierKeys()

		for _, key := range classifierKeys {
			classifier := classifiers.Get(key)

			fmt.Println("Resetting scores for", key)
			classifier.ResetPrevScores()
		}
	})
}

func main() {
	database.InitSqlite()
	database.Migrate()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello, World!")
	})

	http.HandleFunc("/graph", graphDatas)
	http.HandleFunc("/send", sendRequest)

	http.Handle("/metrics", promWrapper(promhttp.Handler()))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Printf("Server is running on port %s\n", port)
	http.ListenAndServe(":"+port, nil)
}
