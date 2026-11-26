package tracks

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/emilthaudal/prism/internal/deskui"
)

// mockSource is prism server --mock: the fixture, with actions applied in memory.
type mockSource struct{ f *deskui.Fixture }

func newMockSource(path string) (*mockSource, error) {
	f, err := deskui.LoadFixture(path)
	if err != nil {
		return nil, err
	}
	return &mockSource{f}, nil
}

func (m *mockSource) changed() bool { return true }

func (m *mockSource) snapshot(ctx context.Context) (any, error) { return m.f.Snapshot(ctx) }

func (m *mockSource) transcript(ctx context.Context, id string) (any, error) {
	es, err := m.f.Transcript(ctx, id)
	if es == nil {
		es = []deskui.Entry{}
	}
	return map[string]any{"entries": es}, err
}

func (m *mockSource) conversation(ctx context.Context, before string) (any, error) {
	s, err := m.f.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	list := s.Conversation
	if before != "" {
		i := slices.IndexFunc(list, func(e deskui.Entry) bool { return e.ID == before })
		if i < 0 {
			return nil, errorf(http.StatusNotFound, "no entry %s", before)
		}
		list = list[:i]
	}
	if len(list) > conversationPage {
		list = list[len(list)-conversationPage:]
	}
	if list == nil {
		list = []deskui.Entry{}
	}
	return map[string]any{"conversation": list}, nil
}

func (m *mockSource) act(ctx context.Context, kind, id, action string, body json.RawMessage) (int, any, error) {
	path := "/v1/" + strings.Join(slices.DeleteFunc([]string{kind, id, action}, func(s string) bool { return s == "" }), "/")
	if len(body) == 0 {
		body = json.RawMessage("{}")
	}
	b, err := m.f.Post(ctx, path, body)
	var fe interface{ Status() int }
	if errors.As(err, &fe) {
		return 0, nil, errorf(fe.Status(), "%s", strings.TrimPrefix(err.Error(), strconv.Itoa(fe.Status())+" "))
	}
	if err != nil {
		return 0, nil, err
	}
	code := http.StatusOK
	if kind == "desk" {
		code = http.StatusAccepted
	}
	return code, json.RawMessage(b), nil
}
