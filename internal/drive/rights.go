package drive

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func rightFields() []string { return strings.Split(rightsAsked, ",") }

type rightsGiven struct{ next http.RoundTripper }

func (t rightsGiven) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := t.next.RoundTrip(req)
	if err != nil || res.StatusCode/100 != 2 || req.URL.Query().Get("alt") == "media" ||
		!strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {

		return res, err
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil {
		return nil, err
	}
	body = fillRights(body)
	res.Body = io.NopCloser(bytes.NewReader(body))
	res.ContentLength = int64(len(body))
	res.Header.Del("Content-Length")
	return res, nil
}

func fillRights(body []byte) []byte {
	if !bytes.Contains(body, []byte(`"capabilities"`)) {
		return body
	}
	dec := json.NewDecoder(bytes.NewReader(body))

	dec.UseNumber()
	var answer any
	if err := dec.Decode(&answer); err != nil {
		return body
	}
	if !fillIn(answer) {
		return body
	}
	out, err := json.Marshal(answer)
	if err != nil {
		return body
	}
	return out
}

func fillIn(v any) bool {
	added := false
	switch v := v.(type) {
	case []any:
		for _, e := range v {
			added = fillIn(e) || added
		}
	case map[string]any:
		for key, e := range v {
			added = fillEntry(key, e) || added
		}
	}
	return added
}

func fillEntry(key string, e any) bool {
	caps, ok := e.(map[string]any)
	if key != "capabilities" || !ok {
		return fillIn(e)
	}
	added := false
	for _, right := range rightFields() {
		if _, came := caps[right]; !came {
			caps[right], added = true, true
		}
	}
	return added
}
