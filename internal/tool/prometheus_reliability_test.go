package tool

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPrometheusResponseContract(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		wantErr    bool
	}{{"upstream-error", `{"status":"error","error":"bad query"}`, 200, true}, {"missing-alerts", `{"status":"success","data":{}}`, 200, true}, {"invalid-json", `{`, 200, true}, {"http-500", `{}`, 500, true}, {"empty-success", `{"status":"success","data":{"alerts":[]}}`, 200, false}} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); w.Write([]byte(tc.body)) }))
			defer s.Close()
			alerts, err := NewPromClient(s.URL).Firing()
			if (err != nil) != tc.wantErr {
				t.Fatalf("alerts=%v err=%v", alerts, err)
			}
			if !tc.wantErr && alerts == nil {
				t.Fatal("success empty dataset must be nonnil")
			}
		})
	}
}
