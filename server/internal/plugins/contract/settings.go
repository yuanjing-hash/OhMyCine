package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
)

const (
	maxSettingsTabs     = 8
	maxSettingsSections = 32
	maxSettingsFields   = 128
)

type SettingsPage struct {
	Version int           `json:"version"`
	Tabs    []SettingsTab `json:"tabs"`
}

type SettingsTab struct {
	ID       string            `json:"id"`
	Title    string            `json:"title"`
	Sections []SettingsSection `json:"sections"`
}

type SettingsSection struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Description string          `json:"description,omitempty"`
	Fields      []SettingsField `json:"fields"`
}

type SettingsField struct {
	Type        string           `json:"type"`
	Key         string           `json:"key,omitempty"`
	Label       string           `json:"label"`
	Description string           `json:"description,omitempty"`
	Placeholder string           `json:"placeholder,omitempty"`
	Options     []SettingsOption `json:"options,omitempty"`
	Minimum     *float64         `json:"minimum,omitempty"`
	Maximum     *float64         `json:"maximum,omitempty"`
}

type SettingsOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type configSchemaDocument struct {
	Type                 string                          `json:"type"`
	AdditionalProperties *bool                           `json:"additionalProperties,omitempty"`
	Properties           map[string]configSchemaProperty `json:"properties"`
}

type configSchemaProperty struct {
	Type    string          `json:"type"`
	Default json.RawMessage `json:"default,omitempty"`
	Enum    []string        `json:"enum,omitempty"`
	Minimum *float64        `json:"minimum,omitempty"`
	Maximum *float64        `json:"maximum,omitempty"`
}

func (page SettingsPage) Validate(schema json.RawMessage) error {
	if page.Version != 1 || len(page.Tabs) == 0 || len(page.Tabs) > maxSettingsTabs {
		return errors.New("settings page version or tab count is invalid")
	}
	document, err := decodeConfigSchema(schema)
	if err != nil {
		return err
	}
	seenTabs := make(map[string]struct{}, len(page.Tabs))
	seenSections := make(map[string]struct{})
	seenFields := make(map[string]struct{})
	sectionCount, fieldCount := 0, 0
	for _, tab := range page.Tabs {
		if !safeSettingsIdentifier(tab.ID) || !safeSettingsText(tab.Title, 80) || len(tab.Sections) == 0 {
			return errors.New("settings tab is invalid")
		}
		if _, duplicate := seenTabs[tab.ID]; duplicate {
			return errors.New("settings tab id is duplicated")
		}
		seenTabs[tab.ID] = struct{}{}
		sectionCount += len(tab.Sections)
		if sectionCount > maxSettingsSections {
			return errors.New("settings section count is too large")
		}
		for _, section := range tab.Sections {
			if !safeSettingsIdentifier(section.ID) || !safeSettingsText(section.Title, 120) || !safeOptionalSettingsText(section.Description, 500) || len(section.Fields) == 0 {
				return errors.New("settings section is invalid")
			}
			if _, duplicate := seenSections[section.ID]; duplicate {
				return errors.New("settings section id is duplicated")
			}
			seenSections[section.ID] = struct{}{}
			fieldCount += len(section.Fields)
			if fieldCount > maxSettingsFields {
				return errors.New("settings field count is too large")
			}
			for _, field := range section.Fields {
				if err := validateSettingsField(field, document, seenFields); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateSettingsField(field SettingsField, schema configSchemaDocument, seen map[string]struct{}) error {
	if !safeSettingsText(field.Label, 120) || !safeOptionalSettingsText(field.Description, 500) || !safeOptionalSettingsText(field.Placeholder, 200) {
		return errors.New("settings field text is invalid")
	}
	if field.Type == "notice" || field.Type == "credential-status" {
		if field.Key != "" || len(field.Options) > 0 || field.Minimum != nil || field.Maximum != nil {
			return errors.New("display-only settings field has invalid bindings")
		}
		return nil
	}
	if !safeSettingsIdentifier(field.Key) {
		return errors.New("settings field key is invalid")
	}
	if _, duplicate := seen[field.Key]; duplicate {
		return errors.New("settings field key is duplicated")
	}
	seen[field.Key] = struct{}{}
	property, exists := schema.Properties[field.Key]
	if !exists {
		return fmt.Errorf("settings field %q is absent from configSchema", field.Key)
	}
	switch field.Type {
	case "switch":
		if property.Type != "boolean" || len(field.Options) > 0 || field.Minimum != nil || field.Maximum != nil {
			return errors.New("switch settings field is invalid")
		}
	case "text":
		if property.Type != "string" || len(field.Options) > 0 || field.Minimum != nil || field.Maximum != nil {
			return errors.New("text settings field is invalid")
		}
	case "number":
		if (property.Type != "number" && property.Type != "integer") || len(field.Options) > 0 {
			return errors.New("number settings field is invalid")
		}
		if field.Minimum != nil && field.Maximum != nil && *field.Minimum > *field.Maximum {
			return errors.New("number settings bounds are reversed")
		}
		if property.Minimum != nil && (field.Minimum == nil || *field.Minimum < *property.Minimum) {
			return errors.New("number settings minimum is broader than configSchema")
		}
		if property.Maximum != nil && (field.Maximum == nil || *field.Maximum > *property.Maximum) {
			return errors.New("number settings maximum is broader than configSchema")
		}
	case "select":
		if property.Type != "string" || len(field.Options) == 0 || len(field.Options) > 64 || field.Minimum != nil || field.Maximum != nil {
			return errors.New("select settings field is invalid")
		}
		seenOptions := make(map[string]struct{}, len(field.Options))
		for _, option := range field.Options {
			if !safeSettingsText(option.Label, 120) || !safeSettingsText(option.Value, 256) {
				return errors.New("select settings option is invalid")
			}
			if _, duplicate := seenOptions[option.Value]; duplicate {
				return errors.New("select settings option is duplicated")
			}
			seenOptions[option.Value] = struct{}{}
			if len(property.Enum) > 0 && !containsString(property.Enum, option.Value) {
				return errors.New("select settings option is outside configSchema enum")
			}
		}
	default:
		return fmt.Errorf("unknown settings field type %q", field.Type)
	}
	return nil
}

func ValidatePluginConfig(schema, raw json.RawMessage) error {
	document, err := decodeConfigSchema(schema)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var values map[string]any
	if err := decoder.Decode(&values); err != nil {
		return errors.New("plugin config must be an object")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("plugin config contains trailing data")
	}
	for key, value := range values {
		property, exists := document.Properties[key]
		if !exists {
			if document.AdditionalProperties != nil && !*document.AdditionalProperties {
				return fmt.Errorf("plugin config property %q is not allowed", key)
			}
			continue
		}
		if err := validateConfigValue(key, value, property); err != nil {
			return err
		}
	}
	return nil
}

func PluginConfigDefaults(schema json.RawMessage) (json.RawMessage, error) {
	document, err := decodeConfigSchema(schema)
	if err != nil {
		return nil, err
	}
	values := make(map[string]any)
	for key, property := range document.Properties {
		if len(property.Default) == 0 {
			continue
		}
		var value any
		decoder := json.NewDecoder(bytes.NewReader(property.Default))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil || validateConfigValue(key, value, property) != nil {
			return nil, fmt.Errorf("plugin config default %q is invalid", key)
		}
		values[key] = value
	}
	return json.Marshal(values)
}

func decodeConfigSchema(raw json.RawMessage) (configSchemaDocument, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var document configSchemaDocument
	if err := decoder.Decode(&document); err != nil || document.Type != "object" {
		return configSchemaDocument{}, errors.New("plugin configSchema is unsupported")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return configSchemaDocument{}, errors.New("plugin configSchema contains trailing data")
	}
	if document.Properties == nil {
		document.Properties = map[string]configSchemaProperty{}
	}
	for key, property := range document.Properties {
		if !safeSettingsIdentifier(key) {
			return configSchemaDocument{}, errors.New("plugin configSchema property is invalid")
		}
		switch property.Type {
		case "boolean", "string", "number", "integer":
		default:
			return configSchemaDocument{}, errors.New("plugin configSchema property type is unsupported")
		}
	}
	return document, nil
}

func validateConfigValue(key string, value any, property configSchemaProperty) error {
	switch property.Type {
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("plugin config property %q must be boolean", key)
		}
	case "string":
		text, ok := value.(string)
		if !ok || len(text) > 4096 || strings.ContainsRune(text, '\x00') {
			return fmt.Errorf("plugin config property %q must be a bounded string", key)
		}
		if len(property.Enum) > 0 && !containsString(property.Enum, text) {
			return fmt.Errorf("plugin config property %q is outside its enum", key)
		}
	case "number", "integer":
		number, ok := value.(json.Number)
		if !ok {
			return fmt.Errorf("plugin config property %q must be numeric", key)
		}
		parsed, err := number.Float64()
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || (property.Type == "integer" && math.Trunc(parsed) != parsed) || (property.Minimum != nil && parsed < *property.Minimum) || (property.Maximum != nil && parsed > *property.Maximum) {
			return fmt.Errorf("plugin config property %q is outside its numeric bounds", key)
		}
	}
	return nil
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func safeSettingsIdentifier(value string) bool {
	if value == "" || len(value) > 80 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (index > 0 && character >= '0' && character <= '9') || (index > 0 && (character == '-' || character == '_' || character == '.')) {
			continue
		}
		return false
	}
	return true
}

func safeSettingsText(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= maximum && !strings.ContainsAny(value, "\x00\r")
}

func safeOptionalSettingsText(value string, maximum int) bool {
	return value == "" || safeSettingsText(value, maximum)
}
