package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rsc.io/qr"

	"smartfactory/internal/checks"
	"smartfactory/internal/mapping"
)

// Forms filled in by hand (/api/checks/{company}): the forms and every machine's rounds, for the
// phone page (/check) and the console; operators send what they wrote, administrators set the
// forms up. And /api/qr draws the code an administrator prints on each machine.
func (s *Server) checkRoutes(mux *http.ServeMux) {
	o := s.ops()
	answer := func(w http.ResponseWriter, v any, err error) {
		if err != nil {
			writeOpError(w, err)
			return
		}
		writeJSON(w, v)
	}
	read := func(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(v); err != nil {
			writeError(w, http.StatusBadRequest, "请求格式不对："+err.Error())
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/checks/{company}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.ChecksOverview(s.caller(r), r.PathValue("company"))
		answer(w, v, err)
	})
	mux.HandleFunc("GET /api/checks/{company}/templates", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.CheckTemplates(s.caller(r), r.PathValue("company"))
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/checks/{company}/forms", func(w http.ResponseWriter, r *http.Request) {
		var f checks.Form
		if !read(w, r, 1<<20, &f) {
			return
		}
		v, err := o.SaveCheckForm(s.caller(r), via(r), r.PathValue("company"), f)
		answer(w, v, err)
	})
	mux.HandleFunc("DELETE /api/checks/{company}/forms/{id}", func(w http.ResponseWriter, r *http.Request) {
		answer(w, map[string]bool{"ok": true}, o.DeleteCheckForm(s.caller(r), via(r), r.PathValue("company"), r.PathValue("id")))
	})
	mux.HandleFunc("GET /api/checks/{company}/rounds", func(w http.ResponseWriter, r *http.Request) {
		var status []string
		for _, x := range strings.Split(r.URL.Query().Get("status"), ",") {
			if x = strings.TrimSpace(x); x != "" {
				status = append(status, x)
			}
		}
		v, err := o.CheckRounds(s.caller(r), r.PathValue("company"), status)
		answer(w, v, err)
	})
	mux.HandleFunc("GET /api/checks/{company}/machines/{plant}/{asset}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.MachineChecks(s.caller(r), r.PathValue("company"), r.PathValue("plant"), r.PathValue("asset"))
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/checks/{company}/submissions", func(w http.ResponseWriter, r *http.Request) {
		var in checks.Input
		// photos come along as data URLs
		if !read(w, r, 8<<20, &in) {
			return
		}
		v, err := o.SubmitCheck(s.caller(r), via(r), r.PathValue("company"), in)
		answer(w, v, err)
	})
	// ?form=&plant=&asset=&from=&to=&bad=1&limit=; format=csv for a spreadsheet, one value a row
	mux.HandleFunc("GET /api/checks/{company}/submissions", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f := checks.Filter{Form: q.Get("form"), Plant: q.Get("plant"), Asset: q.Get("asset"), Bad: q.Get("bad") != ""}
		f.Limit, _ = strconv.Atoi(q.Get("limit"))
		for _, x := range []struct {
			key string
			to  *time.Time
		}{{"from", &f.From}, {"to", &f.To}} {
			if v := q.Get(x.key); v != "" {
				t, ok := mapping.ParseTime(v)
				if !ok {
					writeError(w, http.StatusBadRequest, x.key+" 要是毫秒时间戳或 RFC 3339 时间")
					return
				}
				*x.to = t
			}
		}
		csv := q.Get("format") == "csv"
		if csv && f.Limit == 0 {
			f.Limit = 20000
		}
		company := r.PathValue("company")
		list, err := o.CheckSubmissions(s.caller(r), company, f)
		if err != nil || !csv {
			answer(w, list, err)
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="checks-%s-%s.csv"`, company, time.Now().Format("20060102")))
		s.Checks.WriteCSV(w, list)
	})
	mux.HandleFunc("GET /api/checks/{company}/photos/{id}", func(w http.ResponseWriter, r *http.Request) {
		mime, data, err := o.CheckPhoto(s.caller(r), r.PathValue("company"), r.PathValue("id"))
		if err != nil {
			writeOpError(w, err)
			return
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("Cache-Control", "private, max-age=86400")
		w.Write(data)
	})
	mux.HandleFunc("GET /api/qr", qrCode)
}

// qrCode draws ?text= (a link, up to 600 characters) as a QR code in SVG, with its quiet zone.
func qrCode(w http.ResponseWriter, r *http.Request) {
	text := r.URL.Query().Get("text")
	if text == "" || len(text) > 600 {
		writeError(w, http.StatusBadRequest, "text 要 1～600 个字符")
		return
	}
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var b strings.Builder
	n := c.Size + 8
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges"><rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n, n, n, n)
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if c.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+4, y+4)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write([]byte(b.String()))
}
