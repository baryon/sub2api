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

// TASK-65: DeepSeek's native Responses relay names the model the client asked for whenever DeepSeek's answer names
// another one (an account mapping, or DeepSeek's canonical name for an alias); everything else stays byte for byte.

func TestNameDeepSeekResponsesModelInWireLine(t *testing.T) {
	created := `data: {"type":"response.created","response":{"id":"r1","model":"deepseek-flash","output":[]}}`
	named := `data: {"type":"response.created","response":{"id":"r1","model":"deepseek-v4-flash","output":[]}}`
	const client = "deepseek-v4-flash"
	for _, tc := range []struct {
		name, line, client, want string
	}{
		{"LF ending", created + "\n", client, named + "\n"},
		{"CRLF ending", created + "\r\n", client, named + "\r\n"},
		{"last line without ending", created, client, named},
		{"data without a space", strings.Replace(created, "data: ", "data:", 1) + "\n", client, strings.Replace(named, "data: ", "data:", 1) + "\n"},
		{"data with a tab", strings.Replace(created, "data: ", "data:\t", 1) + "\n", client, strings.Replace(named, "data: ", "data:\t", 1) + "\n"},
		{"top-level model", `data: {"type":"x","model":"deepseek-flash"}` + "\n", client, `data: {"type":"x","model":"deepseek-v4-flash"}` + "\n"},
		{"event line", "event: response.created\n", client, "event: response.created\n"},
		{"blank line", "\n", client, "\n"},
		{"event without a model", `data: {"type":"response.output_text.delta","delta":"deepseek-flash"}` + "\n", client,
			`data: {"type":"response.output_text.delta","delta":"deepseek-flash"}` + "\n"},
		{"model mentioned only in text", `data: {"type":"response.output_text.delta","delta":"\"model\":\"deepseek-flash\""}` + "\n", client,
			`data: {"type":"response.output_text.delta","delta":"\"model\":\"deepseek-flash\""}` + "\n"},
		{"not JSON", `data: "model" deepseek-flash` + "\n", client, `data: "model" deepseek-flash` + "\n"},
		{"answer already names the requested model", named + "\n", client, named + "\n"},
		{"no client name", created + "\n", "", created + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := nameDeepSeekResponsesModelInWireLine([]byte(tc.line), tc.client)
			require.Equal(t, tc.want, string(got))
		})
	}
}

func TestNameDeepSeekResponsesModelForClientKeepsTheOriginal(t *testing.T) {
	body := []byte(`{"id":"r1","object":"response","model":"deepseek-flash","output":[{"type":"reasoning","content":[{"type":"reasoning_text","text":"x"}],"summary":[]}]}`)
	original := string(body)

	named, changed := nameDeepSeekResponsesModelForClient(body, "deepseek-v4-flash")

	require.True(t, changed)
	require.Equal(t, strings.Replace(original, `"model":"deepseek-flash"`, `"model":"deepseek-v4-flash"`, 1), string(named))
	require.Equal(t, original, string(body), "usage and billing keep reading DeepSeek's original bytes")

	same, changed := nameDeepSeekResponsesModelForClient(body, "deepseek-flash")
	require.False(t, changed)
	require.Equal(t, original, string(same))
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
		"deepseek-v4-flash")

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
