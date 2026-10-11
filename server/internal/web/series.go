package web

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"smartfactory/internal/auth"
	"smartfactory/internal/mapping"
	"smartfactory/internal/series"
)

// seriesRoutes answers for what equipment sent: GET /api/series/{company}/{plant}/{asset}
// ?field=temp,power&from=&to=&step= (members and above). from and to are epoch milliseconds or
// RFC 3339 (default: the last hour); step is raw, minute or hour, picked by the range when absent.
func (s *Server) seriesRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/series/{company}/{plant}/{asset}", func(w http.ResponseWriter, r *http.Request) {
		if s.Series == nil {
			writeError(w, http.StatusServiceUnavailable, "这台服务器不保存设备数据（没有数据目录）")
			return
		}
		c := r.PathValue("company")
		if !slices.Contains(s.Engine.CompanyIDs(), c) {
			writeError(w, http.StatusNotFound, "找不到这家公司")
			return
		}
		who := s.caller(r)
		if !who.Can(c, auth.Member) {
			code := http.StatusForbidden
			if who.Key == "" {
				code = http.StatusUnauthorized
			}
			writeError(w, code, "需要这家公司成员以上的令牌")
			return
		}
		qv := r.URL.Query()
		q := series.Query{Company: c, Plant: r.PathValue("plant"), Asset: r.PathValue("asset"), Step: qv.Get("step")}
		for _, f := range strings.Split(qv.Get("field"), ",") {
			if f = strings.TrimSpace(f); f != "" {
				q.Fields = append(q.Fields, f)
			}
		}
		for _, x := range []struct {
			key string
			to  *time.Time
		}{{"from", &q.From}, {"to", &q.To}} {
			if v := qv.Get(x.key); v != "" {
				t, ok := mapping.ParseTime(v)
				if !ok {
					writeError(w, http.StatusBadRequest, x.key+" 要是毫秒时间戳或 RFC 3339 时间")
					return
				}
				*x.to = t
			}
		}
		a, err := s.Series.Read(q)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, a)
	})
}
