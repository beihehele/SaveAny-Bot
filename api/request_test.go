package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTaskRequestRejectsUndefinedFieldsBeforePreparation(t *testing.T) {
	for _, body := range []string{
		`{"type":"tgfiles","storage":"missing","unexpected":false,"params":{"message_links":["https://t.me/test/1"]}}`,
		`{"type":"tgfiles","storage":"missing","params":{}} {}`,
	} {
		rr := httptest.NewRecorder()
		NewHandlers(NewTaskFactory(t.Context())).CreateTaskHandler(rr, httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body)))
		if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "invalid_request") {
			t.Fatalf("undefined request admitted: %d %s", rr.Code, rr.Body.String())
		}
	}
	var params TGFilesParams
	if err := decodeTaskJSON([]byte(`{"message_links":["https://t.me/test/1"],"unexpected":true}`), &params); err == nil {
		t.Fatal("undefined task parameter accepted")
	}
	if err := decodeTaskJSON([]byte(`{"message_links":["https://t.me/test/1"]}`), &params); err != nil || len(params.MessageLinks) != 1 {
		t.Fatalf("current task parameters rejected: %v", err)
	}
}
