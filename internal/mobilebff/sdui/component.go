package sdui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

type ComponentType string

const (
	ComponentTypeForm               ComponentType = "form"
	ComponentTypeTable              ComponentType = "table"
	ComponentTypeList               ComponentType = "list"
	ComponentTypeDetail             ComponentType = "detail"
	ComponentTypeAction             ComponentType = "action"
	ComponentTypeChart              ComponentType = "chart"
	ComponentTypeConfirmDestructive ComponentType = "confirm_destructive"
)

func AllComponentTypes() []ComponentType {
	return []ComponentType{
		ComponentTypeForm,
		ComponentTypeTable,
		ComponentTypeList,
		ComponentTypeDetail,
		ComponentTypeAction,
		ComponentTypeChart,
		ComponentTypeConfirmDestructive,
	}
}

var ErrUnknownComponentType = errors.New("sdui: unknown component type")

type ComponentBase struct {
	Type           ComponentType `json:"type"`
	ID             string        `json:"id"`
	PermissionHint string        `json:"permission_hint,omitempty"`
	Critical       bool          `json:"critical,omitempty"`
}

func (b ComponentBase) Base() ComponentBase { return b }

type Component interface {
	ComponentType() ComponentType
	Base() ComponentBase
}

type DataSource struct {
	Endpoint string `json:"endpoint"`
	Method   string `json:"method,omitempty"`
}

type TableColumn struct {
	Key      string            `json:"key"`
	Label    string            `json:"label"`
	Kind     string            `json:"kind"`
	BadgeMap map[string]string `json:"badge_map,omitempty"`
}

type FormField struct {
	Key           string      `json:"key"`
	Label         string      `json:"label"`
	Kind          string      `json:"kind"`
	Required      bool        `json:"required,omitempty"`
	OptionsSource *DataSource `json:"options_source,omitempty"`
	Options       []string    `json:"options,omitempty"`
	Value         string      `json:"value,omitempty"`
	Placeholder   string      `json:"placeholder,omitempty"`
}

type DetailField struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Kind  string `json:"kind,omitempty"`
}

type ActionRef struct {
	ActionID string `json:"action_id"`
	Label    string `json:"label,omitempty"`
	Style    string `json:"style,omitempty"`
}

type EmptyState struct {
	Text string `json:"text"`
}

type TableComponent struct {
	ComponentBase
	Columns    []TableColumn `json:"columns"`
	RowsSource DataSource    `json:"rows_source"`
	RowActions []ActionRef   `json:"row_actions,omitempty"`
	EmptyState *EmptyState   `json:"empty_state,omitempty"`
}

func (TableComponent) ComponentType() ComponentType { return ComponentTypeTable }

type ListComponent struct {
	ComponentBase
	ItemTemplate string     `json:"item_template"`
	RowsSource   DataSource `json:"rows_source"`
}

func (ListComponent) ComponentType() ComponentType { return ComponentTypeList }

type DetailComponent struct {
	ComponentBase
	DataSource DataSource    `json:"data_source"`
	Fields     []DetailField `json:"fields"`
}

func (DetailComponent) ComponentType() ComponentType { return ComponentTypeDetail }

type FormComponent struct {
	ComponentBase
	Fields       []FormField `json:"fields"`
	SubmitAction ActionRef   `json:"submit_action"`
}

func (FormComponent) ComponentType() ComponentType { return ComponentTypeForm }

type ActionComponent struct {
	ComponentBase
	Label    string `json:"label"`
	ActionID string `json:"action_id"`
	Style    string `json:"style,omitempty"`
}

func (ActionComponent) ComponentType() ComponentType { return ComponentTypeAction }

type ChartComponent struct {
	ComponentBase
	ChartKind    string     `json:"chart_kind"`
	SeriesSource DataSource `json:"series_source"`
	XKey         string     `json:"x_key"`
	YKey         string     `json:"y_key"`
}

func (ChartComponent) ComponentType() ComponentType { return ComponentTypeChart }

type ConfirmDestructiveComponent struct {
	ComponentBase
	ActionID                 string `json:"action_id"`
	Message                  string `json:"message"`
	RequireTypedConfirmation string `json:"require_typed_confirmation,omitempty"`
}

func (ConfirmDestructiveComponent) ComponentType() ComponentType {
	return ComponentTypeConfirmDestructive
}

type Screen struct {
	ID         string
	Title      string
	Components []Component
}

type Envelope struct {
	SDUIVersion int    `json:"sdui_version"`
	Screen      Screen `json:"screen"`
}

type screenWire struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	Components []json.RawMessage `json:"components"`
}

type envelopeWire struct {
	SDUIVersion int        `json:"sdui_version"`
	Screen      screenWire `json:"screen"`
}

func (s Screen) MarshalJSON() ([]byte, error) {
	wire := screenWire{
		ID:         s.ID,
		Title:      s.Title,
		Components: make([]json.RawMessage, len(s.Components)),
	}
	for i, c := range s.Components {
		if c.Base().Type != c.ComponentType() {
			return nil, fmt.Errorf(
				"sdui: component %d (id=%q): Base().Type=%q does not match ComponentType()=%q",
				i, c.Base().ID, c.Base().Type, c.ComponentType(),
			)
		}
		b, err := json.Marshal(c)
		if err != nil {
			return nil, fmt.Errorf("sdui: marshal of component %d (id=%q, type=%q): %w", i, c.Base().ID, c.ComponentType(), err)
		}
		wire.Components[i] = b
	}
	return json.Marshal(wire)
}

func UnmarshalScreen(data []byte) (*Envelope, error) {
	var wire envelopeWire
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return nil, fmt.Errorf("sdui: envelope decode: %w", err)
	}

	components := make([]Component, len(wire.Screen.Components))
	for i, raw := range wire.Screen.Components {
		var typePeek struct {
			Type ComponentType `json:"type"`
		}
		if err := json.Unmarshal(raw, &typePeek); err != nil {
			return nil, fmt.Errorf("sdui: peeking at the type of component %d: %w", i, err)
		}
		c, err := unmarshalComponent(typePeek.Type, raw)
		if err != nil {
			return nil, fmt.Errorf("sdui: component %d: %w", i, err)
		}
		components[i] = c
	}

	return &Envelope{
		SDUIVersion: wire.SDUIVersion,
		Screen: Screen{
			ID:         wire.Screen.ID,
			Title:      wire.Screen.Title,
			Components: components,
		},
	}, nil
}

func unmarshalComponent(t ComponentType, raw json.RawMessage) (Component, error) {
	decodeStrict := func(v any) error {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		return dec.Decode(v)
	}

	switch t {
	case ComponentTypeForm:
		var c FormComponent
		if err := decodeStrict(&c); err != nil {
			return nil, err
		}
		return c, nil
	case ComponentTypeTable:
		var c TableComponent
		if err := decodeStrict(&c); err != nil {
			return nil, err
		}
		return c, nil
	case ComponentTypeList:
		var c ListComponent
		if err := decodeStrict(&c); err != nil {
			return nil, err
		}
		return c, nil
	case ComponentTypeDetail:
		var c DetailComponent
		if err := decodeStrict(&c); err != nil {
			return nil, err
		}
		return c, nil
	case ComponentTypeAction:
		var c ActionComponent
		if err := decodeStrict(&c); err != nil {
			return nil, err
		}
		return c, nil
	case ComponentTypeChart:
		var c ChartComponent
		if err := decodeStrict(&c); err != nil {
			return nil, err
		}
		return c, nil
	case ComponentTypeConfirmDestructive:
		var c ConfirmDestructiveComponent
		if err := decodeStrict(&c); err != nil {
			return nil, err
		}
		return c, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownComponentType, t)
	}
}
