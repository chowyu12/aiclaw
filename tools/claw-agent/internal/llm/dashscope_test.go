package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDetectDialect(t *testing.T) {
	cases := map[string]dialect{
		"https://dashscope.aliyuncs.com/compatible-mode/v1":                  dialectDashScope,
		"https://dashscope-intl.aliyuncs.com/compatible-mode/v1":             dialectDashScope,
		"https://llm-abc123.cn-beijing.maas.aliyuncs.com/compatible-mode/v1": dialectDashScope,
		"https://api.openai.com/v1":                                          dialectOpenAI,
		"https://example-resource.openai.azure.com/openai/v1":                dialectOpenAI,
		"https://dashscope.aliyuncs.com.evil.example/v1":                     dialectOpenAI,
	}
	for base, want := range cases {
		if got := detectDialect(base); got != want {
			t.Errorf("%s: 方言 %v，应为 %v", base, got, want)
		}
	}
}

func TestDashScopeGenerationURL(t *testing.T) {
	const want = "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation"
	for _, base := range []string{
		"https://dashscope.aliyuncs.com/compatible-mode/v1",
		"https://dashscope.aliyuncs.com/compatible-mode/v1/",
		"https://dashscope.aliyuncs.com/api/v1",
		"https://dashscope.aliyuncs.com",
	} {
		if got := dashScopeGenerationURL(base); got != want {
			t.Errorf("%s → %s，应为 %s", base, got, want)
		}
	}
	// 带工作空间的域名要原样保留。
	got := dashScopeGenerationURL("https://llm-x.cn-beijing.maas.aliyuncs.com/compatible-mode/v1")
	if !strings.HasPrefix(got, "https://llm-x.cn-beijing.maas.aliyuncs.com/api/v1/") {
		t.Errorf("工作空间域名丢了：%s", got)
	}
}

func TestDashScopeSize(t *testing.T) {
	if got := dashScopeSize("1024x1024"); got != "1024*1024" {
		t.Errorf("尺寸换算错：%s", got)
	}
	if got := dashScopeSize(""); got != "" {
		t.Errorf("空尺寸应保持为空：%q", got)
	}
}

// dashScopeServer 模拟百炼：兼容路径一律空 404，原生路径按请求形状回结果。
func dashScopeServer(t *testing.T) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var received []map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/services/aigc/multimodal-generation/generation", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request map[string]any
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("请求体不是 JSON：%v", err)
		}
		received = append(received, request)
		if r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		input := request["input"].(map[string]any)
		host := "http://" + r.Host
		switch {
		case input["text"] != nil: // 朗读
			io.WriteString(w, `{"output":{"audio":{"url":"`+host+`/blob/voice.wav","data":""}}}`)
		case strings.Contains(string(body), `"audio":"data:`): // 听写
			io.WriteString(w, `{"output":{"choices":[{"message":{"content":[{"text":"你好，"},{"text":"世界"}]}}]}}`)
		default: // 画图
			io.WriteString(w, `{"output":{"choices":[{"message":{"content":[{"image":"`+host+`/blob/pic.png","type":"image"}]}}]}}`)
		}
	})
	mux.HandleFunc("/blob/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("取图床文件时不该带 Key")
		}
		io.WriteString(w, "BYTES-"+strings.TrimPrefix(r.URL.Path, "/blob/"))
	})
	// 兼容路径：真实的百炼就是这样，空 404。
	mux.HandleFunc("/compatible-mode/v1/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, &received
}

func dashScopeClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	client, err := New(server.URL+"/compatible-mode/v1", "k", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// 测试服务器的主机名是 127.0.0.1，认不出方言；这里指定。
	client.dialect = dialectDashScope
	return client
}

func TestDashScopeGenerateImage(t *testing.T) {
	server, received := dashScopeServer(t)
	client := dashScopeClient(t, server)

	data, err := client.GenerateImage(context.Background(), "qwen-image-3.0", "一只橘猫", "1024x1024")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "BYTES-pic.png" {
		t.Errorf("图片字节不对：%q", data)
	}
	request := (*received)[0]
	if request["model"] != "qwen-image-3.0" {
		t.Errorf("模型没传对：%v", request["model"])
	}
	parameters := request["parameters"].(map[string]any)
	if parameters["size"] != "1024*1024" {
		t.Errorf("尺寸应换成百炼写法：%v", parameters["size"])
	}
	if parameters["watermark"] != false {
		t.Error("应关掉水印")
	}
	messages := request["input"].(map[string]any)["messages"].([]any)
	content := messages[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	if content["text"] != "一只橘猫" {
		t.Errorf("提示词没进 messages：%v", content)
	}
}

func TestDashScopeSpeakDefaultsVoice(t *testing.T) {
	server, received := dashScopeServer(t)
	client := dashScopeClient(t, server)

	audio, ext, err := client.Speak(context.Background(), "qwen3-tts-flash", "你好", "")
	if err != nil {
		t.Fatal(err)
	}
	if string(audio) != "BYTES-voice.wav" || ext != "wav" {
		t.Errorf("语音结果不对：%q %s", audio, ext)
	}
	input := (*received)[0]["input"].(map[string]any)
	if input["voice"] != "Cherry" {
		t.Errorf("没指定音色时应用 Cherry，得到 %v", input["voice"])
	}
	// OpenAI 的默认音色 alloy 在百炼不存在，也要换。
	if _, _, err := client.Speak(context.Background(), "qwen3-tts-flash", "你好", "alloy"); err != nil {
		t.Fatal(err)
	}
	if v := (*received)[1]["input"].(map[string]any)["voice"]; v != "Cherry" {
		t.Errorf("alloy 应换成 Cherry，得到 %v", v)
	}
}

func TestDashScopeTranscribeJoinsText(t *testing.T) {
	server, received := dashScopeServer(t)
	client := dashScopeClient(t, server)

	text, err := client.Transcribe(context.Background(), "qwen3-asr-flash", "录音.mp3", []byte("abc"))
	if err != nil {
		t.Fatal(err)
	}
	if text != "你好，世界" {
		t.Errorf("转写文本应拼起来：%q", text)
	}
	raw, _ := json.Marshal((*received)[0])
	if !strings.Contains(string(raw), `"audio":"data:audio/mpeg;base64,YWJj"`) {
		t.Errorf("音频应以 data URI 内联：%s", raw)
	}
}

func TestDashScopeBusinessErrorInside200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"code":"InvalidParameter","message":"size 不支持"}`)
	}))
	t.Cleanup(server.Close)
	client := dashScopeClient(t, server)
	_, err := client.GenerateImage(context.Background(), "m", "p", "")
	if err == nil || !strings.Contains(err.Error(), "size 不支持") {
		t.Errorf("200 里的业务错误应带出来，得到 %v", err)
	}
}

// OpenAI 方言下打到百炼这种空 404，错误信息要指出「这条接口不存在」而不是只有一个数字。
func TestEmptyNotFoundExplainsItself(t *testing.T) {
	server, _ := dashScopeServer(t)
	client, err := New(server.URL+"/compatible-mode/v1", "k", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GenerateImage(context.Background(), "m", "p", "")
	if err == nil || !strings.Contains(err.Error(), "/images/generations") || !strings.Contains(err.Error(), "别的地址") {
		t.Errorf("空 404 应说明是接口不存在：%v", err)
	}
}
