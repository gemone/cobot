package channel

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	cobot "github.com/cobot-agent/cobot/pkg"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTestFeishuChannel(t *testing.T, fn roundTripFunc) *FeishuChannel {
	t.Helper()
	ch := NewFeishuChannel("feishu:test", FeishuConfig{AppID: "app", AppSecret: "secret"})
	ch.httpClient = &http.Client{Transport: fn, Timeout: 5 * time.Second}
	ch.tokenCache.token = "test-token"
	ch.tokenCache.expire = time.Now().Add(time.Hour)
	return ch
}

func TestSend_InteractiveUsesHTTPPathAndReturnsMessageID(t *testing.T) {
	t.Parallel()

	var gotPath string
	var gotBody string
	ch := newTestFeishuChannel(t, func(req *http.Request) (*http.Response, error) {
		gotPath = req.URL.Path + "?" + req.URL.RawQuery
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		gotBody = string(body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"code":0,"msg":"success","data":{"message_id":"om_test_msg"}}`)),
			Header:     make(http.Header),
		}, nil
	})

	result, err := ch.Send(context.Background(), &cobot.OutboundMessage{
		ReceiveID:     "oc_chat",
		ReceiveType:   "group",
		ReceiveIDType: "chat_id",
		Text:          "before\n\n| col | val |\n| --- | --- |\n| a | b |",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !result.Success {
		t.Fatalf("Send success = false")
	}
	if result.MessageID != "om_test_msg" {
		t.Fatalf("message id = %q, want om_test_msg", result.MessageID)
	}
	if gotPath != "/open-apis/im/v1/messages?receive_id_type=chat_id" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotBody, `"msg_type":"interactive"`) {
		t.Fatalf("payload missing interactive msg_type: %s", gotBody)
	}
	if !strings.Contains(gotBody, `"receive_id":"oc_chat"`) {
		t.Fatalf("payload missing receive_id: %s", gotBody)
	}
	if !strings.Contains(gotBody, `\"elements\"`) {
		t.Fatalf("payload missing card content: %s", gotBody)
	}
}

func TestSend_InteractiveFailsWhenResponseHasNoMessageID(t *testing.T) {
	t.Parallel()

	ch := newTestFeishuChannel(t, func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"code":0,"msg":"success","data":{}}`)),
			Header:     make(http.Header),
		}, nil
	})

	result, err := ch.Send(context.Background(), &cobot.OutboundMessage{
		ReceiveID:     "oc_chat",
		ReceiveIDType: "chat_id",
		Text:          "before\n\n| col | val |\n| --- | --- |\n| a | b |",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if result == nil || result.Success {
		t.Fatalf("expected unsuccessful result, got %+v", result)
	}
	if !strings.Contains(err.Error(), "missing message_id") {
		t.Fatalf("error = %v, want missing message_id", err)
	}
}

func TestSend_ReplyInteractiveUsesReplyEndpoint(t *testing.T) {
	t.Parallel()

	var gotPath string
	var gotBody string
	ch := newTestFeishuChannel(t, func(req *http.Request) (*http.Response, error) {
		gotPath = req.URL.Path
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		gotBody = string(body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"code":0,"msg":"success","data":{"message_id":"om_reply"}}`)),
			Header:     make(http.Header),
		}, nil
	})

	result, err := ch.Send(context.Background(), &cobot.OutboundMessage{
		ReceiveID:        "oc_chat",
		ReceiveIDType:    "chat_id",
		Text:             "before\n\n| col | val |\n| --- | --- |\n| a | b |",
		ReplyToMessageID: "om_parent",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.MessageID != "om_reply" {
		t.Fatalf("message id = %q, want om_reply", result.MessageID)
	}
	if gotPath != "/open-apis/im/v1/messages/om_parent/reply" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotBody, `"reply_in_thread":false`) {
		t.Fatalf("reply payload missing reply_in_thread: %s", gotBody)
	}
}
