package app

import (
	"bytes"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func multipartRequest(t *testing.T, payload string, files map[string][]byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("payload", payload)
	for name, data := range files {
		part, err := writer.CreateFormFile("attachments", name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(data)
	}
	_ = writer.Close()
	request := httptest.NewRequest(http.MethodPost, "/", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func TestReadTicketRequestAcceptsImages(t *testing.T) {
	var input struct {
		Body string `json:"body"`
	}
	recorder := httptest.NewRecorder()
	uploads, ok := readTicketRequest(recorder, multipartRequest(t, `{"body":"截图如下"}`, map[string][]byte{"../../screen shot.png": pngBytes(t)}), 5, &input)
	if !ok || len(uploads) != 1 || input.Body != "截图如下" {
		t.Fatalf("ok=%v uploads=%d body=%q response=%s", ok, len(uploads), input.Body, recorder.Body)
	}
	if uploads[0].ContentType != "image/png" || uploads[0].FileName != "screen shot.png" {
		t.Fatalf("upload = %+v", uploads[0])
	}
}

func TestReadTicketRequestRejectsNonImagesAndOversize(t *testing.T) {
	var input struct {
		Body string `json:"body"`
	}
	recorder := httptest.NewRecorder()
	if _, ok := readTicketRequest(recorder, multipartRequest(t, `{"body":"x"}`, map[string][]byte{"shot.png": []byte("<svg onload=alert(1)>")}), 5, &input); ok || recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("disguised non-image accepted: %d %s", recorder.Code, recorder.Body)
	}
	recorder = httptest.NewRecorder()
	large := append(pngBytes(t), bytes.Repeat([]byte{0}, 1<<20+10)...)
	if _, ok := readTicketRequest(recorder, multipartRequest(t, `{"body":"x"}`, map[string][]byte{"big.png": large}), 1, &input); ok {
		t.Fatalf("image over the limit accepted: %d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	if _, ok := readTicketRequest(recorder, multipartRequest(t, `{"body":"x","extra":1}`, nil), 5, &input); ok || recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown payload field accepted: %d", recorder.Code)
	}
}

func TestReadTicketRequestStillTakesJSON(t *testing.T) {
	var input struct {
		Body string `json:"body"`
	}
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"body":"hello"}`))
	request.Header.Set("Content-Type", "application/json")
	uploads, ok := readTicketRequest(httptest.NewRecorder(), request, 5, &input)
	if !ok || len(uploads) != 0 || input.Body != "hello" {
		t.Fatalf("json request: ok=%v uploads=%d body=%q", ok, len(uploads), input.Body)
	}
}

func TestHostedVirtualizationReadsDriverAndJSONShapes(t *testing.T) {
	for _, raw := range []map[string]any{
		{"runtimes": []string{"lxc", "podman", "kvm"}},
		{"runtimes": []any{"podman", "lxc", "podman"}},
	} {
		got := hostedVirtualization(raw)
		if len(got) != 2 || !containsString(got, "lxc") || !containsString(got, "podman") {
			t.Fatalf("hostedVirtualization(%v) = %v", raw, got)
		}
	}
	if got := hostedVirtualization(map[string]any{}); len(got) != 0 {
		t.Fatalf("empty runtimes = %v", got)
	}
}
