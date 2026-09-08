// Package report renders one or more result documents into a single
// self-contained HTML file.
package report

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"

	"github.com/binwu/postgres-on-gke/stress/internal/metrics"
)

//go:embed template.html assets/chart.min.js assets/style.css assets/report.js
var files embed.FS

var tmpl = template.Must(template.ParseFS(files, "template.html"))

type page struct {
	Title   string
	ChartJS template.JS
	CSS     template.CSS
	AppJS   template.JS
	Data    template.JS
}

// Render writes the HTML report for runs (at least one) to w.
func Render(w io.Writer, runs []*metrics.Results) error {
	if len(runs) == 0 {
		return fmt.Errorf("no results to render")
	}
	data, err := json.Marshal(runs) // json.Marshal escapes <, > and & so it is safe inside <script>
	if err != nil {
		return err
	}
	chart, _ := files.ReadFile("assets/chart.min.js")
	css, _ := files.ReadFile("assets/style.css")
	app, _ := files.ReadFile("assets/report.js")
	title := "pgstress: " + runs[0].Scenario
	if len(runs) > 1 {
		title = fmt.Sprintf("pgstress: %d runs compared", len(runs))
	}
	return tmpl.Execute(w, page{
		Title:   title,
		ChartJS: template.JS(chart), //nolint:gosec // vendored, trusted asset
		CSS:     template.CSS(css),
		AppJS:   template.JS(app), //nolint:gosec // our own embedded script
		Data:    template.JS(data),
	})
}

// LoadResults reads results JSON files.
func LoadResults(paths []string) ([]*metrics.Results, error) {
	var runs []*metrics.Results
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var r metrics.Results
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
		runs = append(runs, &r)
	}
	return runs, nil
}
