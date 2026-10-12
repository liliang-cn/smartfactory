package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"smartfactory/internal/ops"
)

// The value report (/api/value/{company}): what the twin and its AI did for the company over a
// period, for members and above; ?format=csv is the export a salesperson hands over.
func (s *Server) valueRoutes(mux *http.ServeMux) {
	o := s.ops()
	mux.HandleFunc("GET /api/value/{company}", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		p, err := ops.ParsePeriod(q.Get("days"), q.Get("from"), q.Get("to"), time.Now())
		if err != nil {
			writeOpError(w, err)
			return
		}
		v, err := o.Value(r.Context(), s.caller(r), r.PathValue("company"), p)
		if err != nil {
			writeOpError(w, err)
			return
		}
		if q.Get("format") == "csv" {
			name := fmt.Sprintf("value-%s-%s-%s.csv", v.Company, p.From.Format("20060102"), p.To.Add(-time.Second).Format("20060102"))
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
			w.Write(v.CSV())
			return
		}
		writeJSON(w, v)
	})
}

var consultClient = &http.Client{Timeout: 20 * time.Second}

// consult reads a company's consulting record from its advisor, as the identity its viewers'
// reads go out as.
func (s *Server) consult(ctx context.Context, company string) ([]byte, error) {
	a, ok := s.AdvisorAPI[company]
	if !ok || a.API == "" {
		return nil, ops.ErrNoAdvisor
	}
	req, err := http.NewRequestWithContext(ctx, "GET", a.API+"/v1/consult", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	res, err := consultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("顾问服务返回 %d", res.StatusCode)
	}
	return b, nil
}
