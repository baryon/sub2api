package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TASK-65: DeepSeek's native Responses relay names the model the client asked for when the account maps it to
// another upstream name; everything else on the line stays byte for byte.

func TestRenameDeepSeekResponsesModelInWireLine(t *testing.T) {
	svc := &OpenAIGatewayService{}
	created := `data: {"type":"response.created","response":{"id":"r1","model":"deepseek-flash","output":[]}}`
	renamed := `data: {"type":"response.created","response":{"id":"r1","model":"deepseek-v4-flash","output":[]}}`
	for _, tc := range []struct {
		name, line, upstream, client, want string
	}{
		{"LF ending", created + "\n", "deepseek-flash", "deepseek-v4-flash", renamed + "\n"},
		{"CRLF ending", created + "\r\n", "deepseek-flash", "deepseek-v4-flash", renamed + "\r\n"},
		{"last line without ending", created, "deepseek-flash", "deepseek-v4-flash", renamed},
		{"data without a space", strings.Replace(created, "data: ", "data:", 1) + "\n", "deepseek-flash", "deepseek-v4-flash",
			strings.Replace(renamed, "data: ", "data:", 1) + "\n"},
		{"data with a tab", strings.Replace(created, "data: ", "data:\t", 1) + "\n", "deepseek-flash", "deepseek-v4-flash",
			strings.Replace(renamed, "data: ", "data:\t", 1) + "\n"},
		{"event line", "event: response.created\n", "deepseek-flash", "deepseek-v4-flash", "event: response.created\n"},
		{"blank line", "\n", "deepseek-flash", "deepseek-v4-flash", "\n"},
		{"event without a model", `data: {"type":"response.output_text.delta","delta":"deepseek-flash"}` + "\n", "deepseek-flash", "deepseek-v4-flash",
			`data: {"type":"response.output_text.delta","delta":"deepseek-flash"}` + "\n"},
		{"not JSON", "data: deepseek-flash\n", "deepseek-flash", "deepseek-v4-flash", "data: deepseek-flash\n"},
		{"unmapped", created + "\n", "deepseek-flash", "deepseek-flash", created + "\n"},
		{"no client name", created + "\n", "deepseek-flash", "", created + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := svc.renameDeepSeekResponsesModelInWireLine([]byte(tc.line), tc.upstream, tc.client)
			require.Equal(t, tc.want, string(got))
		})
	}
}

func TestHandleDeepSeekResponsesStreamRenamesModelAcrossChunksAndCRLF(t *testing.T) {
	gin.SetMode(gin.TestMode)
	wire := "event: response.created\r\n" +
		`data: {"type":"response.created","response":{"id":"r1","status":"in_progress","model":"deepseek-flash","output":[]}}` + "\r\n\r\n" +
		"event: response.output_text.delta\r\n" +
		`data: {"type":"response.output_text.delta","delta":"I am deepseek-flash.","item_id":"m1","output_index":0}` + "\r\n\r\n" +
		"event: response.completed\r\n" +
		`data: {"type":"response.completed","response":{"id":"r1","status":"completed","model":"deepseek-flash","output":[],"usage":{"input_tokens":3,"output_tokens":4}}}` + "\r\n\r\n"
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		// Seven bytes per read: lines arrive split across reads and are relayed as they complete.
		Body: io.NopCloser(&deepSeekModelNameChunkReader{data: []byte(wire), size: 7}),
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	svc := &OpenAIGatewayService{cfg: &config.Config{}}

	result, err := svc.handleDeepSeekResponsesStream(context.Background(), resp, c, deepSeekForwardTestAccount(), time.Now(),
		"deepseek-v4-flash", "deepseek-flash")

	require.NoError(t, err)
	require.Equal(t, "response.completed", result.terminalEvent)
	require.Equal(t, 3, result.usage.InputTokens)
	require.Equal(t, 4, result.usage.OutputTokens)
	require.Equal(t, strings.ReplaceAll(wire, `"model":"deepseek-flash"`, `"model":"deepseek-v4-flash"`), recorder.Body.String())
}

type deepSeekModelNameChunkReader struct {
	data []byte
	size int
}

func (r *deepSeekModelNameChunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := r.size
	if n > len(p) {
		n = len(p)
	}
	if n > len(r.data) {
		n = len(r.data)
	}
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}
