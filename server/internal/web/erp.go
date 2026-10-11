package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"smartfactory/internal/erp"
)

// The order API (/api/erp/{company}/orders): what an ERP calls with its own token to send and
// withdraw production orders and read how they went, and what the console calls too. The
// connection's settings are under /api/admin/erp.
func (s *Server) erpRoutes(mux *http.ServeMux) {
	o := s.ops()
	answer := func(w http.ResponseWriter, v any, err error) {
		if err != nil {
			writeOpError(w, err)
			return
		}
		writeJSON(w, v)
	}
	mux.HandleFunc("GET /api/erp/{company}/orders", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		var status []string
		for _, st := range strings.Split(q.Get("status"), ",") {
			if st = strings.TrimSpace(st); st != "" {
				status = append(status, st)
			}
		}
		v, err := o.Orders(s.caller(r), r.PathValue("company"), erp.Filter{Status: status, Q: q.Get("q"), Limit: limit})
		answer(w, v, err)
	})
	// one order, a list of them, or {"orders": [...]}
	mux.HandleFunc("POST /api/erp/{company}/orders", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
		if err != nil {
			writeError(w, http.StatusBadRequest, "请求太大或没读完："+err.Error())
			return
		}
		list, err := ordersIn(b)
		if err != nil {
			writeError(w, http.StatusBadRequest, "请求格式不对："+err.Error())
			return
		}
		v, err := o.SubmitOrders(s.caller(r), viaAPI(r), r.PathValue("company"), list)
		answer(w, v, err)
	})
	mux.HandleFunc("GET /api/erp/{company}/orders/{no}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.Order(s.caller(r), r.PathValue("company"), r.PathValue("no"))
		answer(w, v, err)
	})
	mux.HandleFunc("DELETE /api/erp/{company}/orders/{no}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.CancelOrder(s.caller(r), viaAPI(r), r.PathValue("company"), r.PathValue("no"))
		answer(w, v, err)
	})

	mux.HandleFunc("GET /api/admin/erp/{company}", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.ERP(s.caller(r), r.PathValue("company"))
		answer(w, v, err)
	})
	mux.HandleFunc("PUT /api/admin/erp/{company}", func(w http.ResponseWriter, r *http.Request) {
		var cfg erp.Config
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&cfg); err != nil {
			writeError(w, http.StatusBadRequest, "请求格式不对："+err.Error())
			return
		}
		v, err := o.SetERP(s.caller(r), via(r), r.PathValue("company"), cfg)
		answer(w, v, err)
	})
	// ?dry=1: what a pull reads and where each order would run, nothing stored
	mux.HandleFunc("POST /api/admin/erp/{company}/pull", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.PullERP(r.Context(), s.caller(r), via(r), r.PathValue("company"), r.URL.Query().Get("dry") != "")
		answer(w, v, err)
	})
	mux.HandleFunc("POST /api/admin/erp/{company}/secret", func(w http.ResponseWriter, r *http.Request) {
		v, err := o.RotateERPSecret(s.caller(r), via(r), r.PathValue("company"))
		answer(w, v, err)
	})
}

func ordersIn(b []byte) ([]map[string]any, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	if m, ok := v.(map[string]any); ok {
		if list, ok := m["orders"].([]any); ok {
			v = list
		} else {
			return []map[string]any{m}, nil
		}
	}
	list, ok := v.([]any)
	if !ok {
		return nil, errNotOrders
	}
	out := make([]map[string]any, len(list))
	for i, x := range list {
		if out[i], ok = x.(map[string]any); !ok {
			return nil, errNotOrders
		}
	}
	return out, nil
}

type notOrders struct{}

func (notOrders) Error() string { return "要一张工单（对象）、工单列表，或 {\"orders\": [...]}" }

var errNotOrders = notOrders{}

// viaAPI is where an order request came from: the console and the command line say so, the
// rest is the ERP (or another system) calling the API.
func viaAPI(r *http.Request) string {
	switch r.Header.Get("X-SF-Via") {
	case "cli":
		return "cli"
	case "page":
		return "page"
	}
	return "api"
}
