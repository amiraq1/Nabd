package agent_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nabd/internal/agent"
	"nabd/internal/provider"
)

type recordSink struct {
	events []agent.Event
}

func (s *recordSink) Emit(e agent.Event) error {
	s.events = append(s.events, e)
	return nil
}

func TestEmptyTurnEmitsNotice(t *testing.T) {
	t.Run("finish_reason length blames NABD_MAX_TOKENS", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"length"}]}` + "\n\n" +
				`data: [DONE]` + "\n\n"))
		}))
		defer srv.Close()

		sink := &recordSink{}
		prov := &provider.OpenAICompat{Key: "test", Model: "m", BaseURL: srv.URL, Client: &http.Client{}}
		l := &agent.Loop{
			Provider: prov,
			Tools:    noTools{},
			Budget:   agent.NewBudget(),
			Gate:     noTools{},
			Human:    noTools{},
			Sink:     sink,
		}

		err := l.Run(context.Background(), "hello")
		if err != nil {
			t.Fatalf("unexpected Run error: %v", err)
		}

		var foundNotice bool
		for _, ev := range sink.events {
			if ev.Type == agent.Notice {
				if strings.Contains(ev.Text, "NABD_MAX_TOKENS") && strings.Contains(ev.Text, "1024") {
					foundNotice = true
					if ev.NoticeCategory != agent.NoticeCategoryLengthLimit {
						t.Errorf("expected NoticeCategoryLengthLimit, got %v", ev.NoticeCategory)
					}
					if strings.Contains(ev.Text, "upstream issue") {
						t.Errorf("must not blame upstream issue when finish_reason=length: %q", ev.Text)
					}
					break
				}
			}
		}

		if !foundNotice {
			t.Fatalf("expected Notice naming NABD_MAX_TOKENS and 1024, got events: %+v", sink.events)
		}
	})

	t.Run("0 prompt and 0 completion tokens with finish_reason stop reports upstream issue", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":0,"completion_tokens":0}}` + "\n\n" +
				`data: [DONE]` + "\n\n"))
		}))
		defer srv.Close()

		sink := &recordSink{}
		prov := &provider.OpenAICompat{Key: "test", Model: "m", BaseURL: srv.URL, Client: &http.Client{}}
		l := &agent.Loop{
			Provider: prov,
			Tools:    noTools{},
			Budget:   agent.NewBudget(),
			Gate:     noTools{},
			Human:    noTools{},
			Sink:     sink,
		}

		err := l.Run(context.Background(), "hello")
		if err != nil {
			t.Fatalf("unexpected Run error: %v", err)
		}

		var foundNotice bool
		for _, ev := range sink.events {
			if ev.Type == agent.Notice {
				if strings.Contains(ev.Text, "provider returned an empty response (upstream issue)") {
					foundNotice = true
					if strings.Contains(ev.Text, "NABD_MAX_TOKENS") {
						t.Errorf("must not blame NABD_MAX_TOKENS on upstream empty response: %q", ev.Text)
					}
					if strings.Contains(ev.Text, "limit") {
						t.Errorf("must not suggest raising limit on upstream empty response: %q", ev.Text)
					}
					break
				}
			}
		}

		if !foundNotice {
			t.Fatalf("expected Notice reporting upstream issue, got events: %+v", sink.events)
		}
	})
}

func TestEmptyTurnEmitsNoticeWithCustomMaxTokens(t *testing.T) {
	t.Setenv("NABD_MAX_TOKENS", "2048")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"length"}]}` + "\n\n" +
			`data: [DONE]` + "\n\n"))
	}))
	defer srv.Close()

	sink := &recordSink{}
	prov := &provider.OpenAICompat{Key: "test", Model: "m", BaseURL: srv.URL, Client: &http.Client{}}
	l := &agent.Loop{
		Provider: prov,
		Tools:    noTools{},
		Budget:   agent.NewBudget(),
		Gate:     noTools{},
		Human:    noTools{},
		Sink:     sink,
	}

	err := l.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("unexpected Run error: %v", err)
	}

	var foundNotice bool
	for _, ev := range sink.events {
		if ev.Type == agent.Notice {
			if strings.Contains(ev.Text, "NABD_MAX_TOKENS") && strings.Contains(ev.Text, "2048") {
				foundNotice = true
				break
			}
		}
	}

	if !foundNotice {
		t.Fatalf("expected Notice naming NABD_MAX_TOKENS and 2048, got events: %+v", sink.events)
	}
}
