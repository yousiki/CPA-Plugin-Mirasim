package config

import (
	"reflect"
	"testing"
)

func TestParseModelIDs(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []ModelSpec
	}{
		{name: "empty falls back to the built-in catalogue", raw: "  ", want: DefaultModels},
		{name: "junk only falls back too", raw: ",, ,", want: DefaultModels},
		{
			name: "id with a context length",
			raw:  "claude-opus-5:1000000",
			want: []ModelSpec{{ID: "claude-opus-5", ContextLength: 1_000_000}},
		},
		{
			name: "bare ids and whitespace",
			raw:  " kimi-k3 , gpt-4o-mini-openrouter ",
			want: []ModelSpec{{ID: "kimi-k3"}, {ID: "gpt-4o-mini-openrouter"}},
		},
		{
			name: "a non-numeric suffix stays part of the id",
			raw:  "vendor/model:latest",
			want: []ModelSpec{{ID: "vendor/model:latest"}},
		},
		{
			name: "only the last colon is the separator",
			raw:  "vendor/model:v2:200000",
			want: []ModelSpec{{ID: "vendor/model:v2", ContextLength: 200_000}},
		},
		{
			name: "a zero context length is not a length",
			raw:  "model:0",
			want: []ModelSpec{{ID: "model:0"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ParseModelIDs(test.raw); !reflect.DeepEqual(got, test.want) {
				t.Errorf("ParseModelIDs(%q) = %v, want %v", test.raw, got, test.want)
			}
		})
	}
}

func TestDecodeAppliesDefaults(t *testing.T) {
	cfg, err := Decode([]byte("relay_url: \"https://relay.example.com/\"\nrefresh_interval_seconds: 0\n"))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if cfg.RelayURL != "https://relay.example.com" {
		t.Errorf("RelayURL = %q, want the trailing slash trimmed", cfg.RelayURL)
	}
	// A zero or negative interval would leave the host with no refresh lead at all.
	if cfg.RefreshIntervalSeconds != Default().RefreshIntervalSeconds {
		t.Errorf("RefreshIntervalSeconds = %d, want the default %d",
			cfg.RefreshIntervalSeconds, Default().RefreshIntervalSeconds)
	}
	if cfg.HTTPTimeoutSeconds != Default().HTTPTimeoutSeconds {
		t.Errorf("HTTPTimeoutSeconds = %d, want the default %d",
			cfg.HTTPTimeoutSeconds, Default().HTTPTimeoutSeconds)
	}
	if !reflect.DeepEqual(cfg.Models, DefaultModels) {
		t.Errorf("Models = %v, want the built-in catalogue", cfg.Models)
	}
}

// Models is parsed from model_ids, never read from YAML: a `models:` key in the config
// must not smuggle in a catalogue that ParseModelIDs never validated.
func TestDecodeIgnoresAModelsKey(t *testing.T) {
	cfg, err := Decode([]byte("models:\n  - id: bogus\n    contextlength: 5\n"))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !reflect.DeepEqual(cfg.Models, DefaultModels) {
		t.Errorf("Models = %v, want the built-in catalogue", cfg.Models)
	}
}

func TestDecodeParsesModelIDs(t *testing.T) {
	cfg, err := Decode([]byte("model_ids: \"only-this:1234\"\n"))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	want := []ModelSpec{{ID: "only-this", ContextLength: 1234}}
	if !reflect.DeepEqual(cfg.Models, want) {
		t.Errorf("Models = %v, want %v", cfg.Models, want)
	}
	if got := cfg.ModelIDList(); !reflect.DeepEqual(got, []string{"only-this"}) {
		t.Errorf("ModelIDList() = %v, want [only-this]", got)
	}
}
