package management

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

	"github.com/yousiki/CPA-Plugin-Mirasim/internal/config"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/hostapi"
	"github.com/yousiki/CPA-Plugin-Mirasim/internal/routes"
)

// fakeHost answers the auth host callbacks from an in-memory file map, keyed by name.
type fakeHost struct {
	files map[string]json.RawMessage
	saves int
}

func withFakeHost(t *testing.T, files map[string]json.RawMessage) *fakeHost {
	t.Helper()
	fake := &fakeHost{files: files}
	hostapi.SetCall(func(method string, payload any) (json.RawMessage, error) {
		switch method {
		case pluginabi.MethodHostAuthList:
			entries := make([]pluginapi.HostAuthFileEntry, 0, len(fake.files))
			for name := range fake.files {
				entries = append(entries, pluginapi.HostAuthFileEntry{
					AuthIndex: name,
					Name:      name,
					Provider:  config.PluginID,
				})
			}
			return json.Marshal(map[string]any{"files": entries})
		case pluginabi.MethodHostAuthGet:
			req := payload.(pluginapi.HostAuthGetRequest)
			return json.Marshal(pluginapi.HostAuthGetResponse{
				AuthIndex: req.AuthIndex,
				Name:      req.AuthIndex,
				JSON:      fake.files[req.AuthIndex],
			})
		case pluginabi.MethodHostAuthSave:
			req := payload.(pluginapi.HostAuthSaveRequest)
			fake.files[req.Name] = req.JSON
			fake.saves++
			return json.Marshal(map[string]any{"ok": true})
		default:
			t.Fatalf("unexpected host callback %s", method)
			return nil, nil
		}
	})
	t.Cleanup(func() { hostapi.SetCall(nil) })
	return fake
}

func routingAction(t *testing.T, op, value string) map[string]any {
	t.Helper()
	resp := call(t, routes.ResourcePrefix+routes.StatusAction, url.Values{
		"token": {"s3cret"},
		"op":    {op},
		"value": {value},
	})
	var body map[string]any
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatalf("decode action body: %v (%s)", err, resp.Body)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, resp.Body)
	}
	return body
}

func TestSetWeightAllWritesAndClears(t *testing.T) {
	cfg := config.Default()
	cfg.ConsoleToken = "s3cret"
	withConfig(t, cfg)
	fake := withFakeHost(t, map[string]json.RawMessage{
		"mirasim-a@example.com.json": json.RawMessage(`{"type":"mirasim","email":"a@example.com","access_token":"x","refresh_token":"y"}`),
		"mirasim-b@example.com.json": json.RawMessage(`{"type":"mirasim","email":"b@example.com","access_token":"x","refresh_token":"y","weight":5}`),
	})

	body := routingAction(t, "set_weight_all", "5")
	if body["changed"].(float64) != 1 || body["skipped"].(float64) != 1 {
		t.Fatalf("set: changed/skipped = %v/%v, want 1/1", body["changed"], body["skipped"])
	}
	var saved map[string]any
	_ = json.Unmarshal(fake.files["mirasim-a@example.com.json"], &saved)
	if saved["weight"].(float64) != 5 {
		t.Fatalf("saved file weight = %v, want 5", saved["weight"])
	}
	if saved["email"] != "a@example.com" || saved["refresh_token"] != "y" {
		t.Fatalf("save dropped unrelated keys: %v", saved)
	}

	// An empty value resets to default by deleting the key from every credential.
	body = routingAction(t, "set_weight_all", "")
	if body["changed"].(float64) != 2 {
		t.Fatalf("clear: changed = %v, want 2", body["changed"])
	}
	for name, raw := range fake.files {
		var file map[string]any
		_ = json.Unmarshal(raw, &file)
		if _, ok := file["weight"]; ok {
			t.Errorf("%s still carries weight after clear: %v", name, file)
		}
	}
}

func TestSetPriorityAllAcceptsNegativeValues(t *testing.T) {
	cfg := config.Default()
	cfg.ConsoleToken = "s3cret"
	withConfig(t, cfg)
	fake := withFakeHost(t, map[string]json.RawMessage{
		"mirasim-a@example.com.json": json.RawMessage(`{"type":"mirasim","email":"a@example.com","access_token":"x","refresh_token":"y"}`),
	})

	body := routingAction(t, "set_priority_all", "-1")
	if body["changed"].(float64) != 1 {
		t.Fatalf("changed = %v, want 1", body["changed"])
	}
	var saved map[string]any
	_ = json.Unmarshal(fake.files["mirasim-a@example.com.json"], &saved)
	if saved["priority"].(float64) != -1 {
		t.Fatalf("saved priority = %v, want -1", saved["priority"])
	}
}

func TestSetWeightAllRejectsBadValues(t *testing.T) {
	cfg := config.Default()
	cfg.ConsoleToken = "s3cret"
	withConfig(t, cfg)
	fake := withFakeHost(t, map[string]json.RawMessage{})

	for _, value := range []string{"-1", "1000001", "abc"} {
		resp := call(t, routes.ResourcePrefix+routes.StatusAction, url.Values{
			"token": {"s3cret"},
			"op":    {"set_weight_all"},
			"value": {value},
		})
		if resp.StatusCode != 400 {
			t.Errorf("value=%s: status = %d, want 400", value, resp.StatusCode)
		}
	}
	if fake.saves != 0 {
		t.Errorf("a rejected value must not reach the host, saves = %d", fake.saves)
	}
}
