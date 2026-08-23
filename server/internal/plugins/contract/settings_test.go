package contract

import (
	"encoding/json"
	"testing"
)

func settingsSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"quality":{"type":"string","enum":["auto","1080p"],"default":"auto"},"parallel":{"type":"integer","minimum":1,"maximum":4,"default":2},"enabled":{"type":"boolean","default":true}}}`)
}

func validSettingsPage() SettingsPage {
	minimum, maximum := 1.0, 4.0
	fields := []SettingsField{
		{Type: "select", Key: "quality", Label: "清晰度", Options: []SettingsOption{{Label: "自动", Value: "auto"}, {Label: "1080P", Value: "1080p"}}},
		{Type: "number", Key: "parallel", Label: "并发数", Minimum: &minimum, Maximum: &maximum},
		{Type: "switch", Key: "enabled", Label: "启用"},
	}
	section := SettingsSection{ID: "download", Title: "下载", Fields: fields}
	tab := SettingsTab{ID: "general", Title: "常规", Sections: []SettingsSection{section}}
	return SettingsPage{Version: 1, Tabs: []SettingsTab{tab}}
}

func TestSettingsPageRejectsUnsafeBindings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SettingsPage)
	}{
		{"unknown component", func(page *SettingsPage) { page.Tabs[0].Sections[0].Fields[0].Type = "html" }},
		{"duplicate key", func(page *SettingsPage) { page.Tabs[0].Sections[0].Fields[2].Key = "quality" }},
		{"missing schema field", func(page *SettingsPage) { page.Tabs[0].Sections[0].Fields[2].Key = "missing" }},
		{"option outside enum", func(page *SettingsPage) {
			page.Tabs[0].Sections[0].Fields[0].Options = append(page.Tabs[0].Sections[0].Fields[0].Options, SettingsOption{Label: "4K", Value: "4k"})
		}},
		{"minimum broadens schema", func(page *SettingsPage) { value := 0.0; page.Tabs[0].Sections[0].Fields[1].Minimum = &value }},
		{"maximum broadens schema", func(page *SettingsPage) { value := 8.0; page.Tabs[0].Sections[0].Fields[1].Maximum = &value }},
		{"missing minimum broadens schema", func(page *SettingsPage) { page.Tabs[0].Sections[0].Fields[1].Minimum = nil }},
		{"missing maximum broadens schema", func(page *SettingsPage) { page.Tabs[0].Sections[0].Fields[1].Maximum = nil }},
		{"duplicate section id across tabs", func(page *SettingsPage) {
			copyTab := page.Tabs[0]
			copyTab.ID = "advanced"
			page.Tabs = append(page.Tabs, copyTab)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			page := validSettingsPage()
			test.mutate(&page)
			if err := page.Validate(settingsSchema()); err == nil {
				t.Fatal("unsafe settings page was accepted")
			}
		})
	}
}

func TestSettingsPageAndDefaultsUseConfigSchema(t *testing.T) {
	if err := validSettingsPage().Validate(settingsSchema()); err != nil {
		t.Fatal(err)
	}
	defaults, err := PluginConfigDefaults(settingsSchema())
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePluginConfig(settingsSchema(), defaults); err != nil {
		t.Fatalf("defaults did not pass normal config validation: %v", err)
	}
	invalid := json.RawMessage(`{"type":"object","properties":{"parallel":{"type":"integer","minimum":1,"default":0}}}`)
	if _, err := PluginConfigDefaults(invalid); err == nil {
		t.Fatal("invalid default was accepted")
	}
}
