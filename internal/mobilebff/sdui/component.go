// Package sdui defines the Android app's Server-Driven UI vocabulary: a
// CLOSED set of 7 component types the client knows how to render, plus the
// screen envelope that carries them.
//
// Why closed, and why that is the package's most important decision: the
// previous mobile attempt died of silent divergence between the web
// surface and the mobile one. SDUI exists to close that divergence for good
// across the long tail of admin screens — but it only works if the
// vocabulary stays closed. A generic container/layout type with nested
// children (`children: [component, ...]`) reintroduces exactly the
// problem the closure solves: it becomes a programming language in
// JSON, with no type checker, no debugger, and every future divergence
// between server and client goes back to being a silent bug instead of a
// Kotlin compile error.
//
// The 7 types are permanent: form, table, list, detail, action, chart,
// confirm_destructive. If a new screen needs something none of the 7
// types expresses, that is a sign for a native `:feature-*` module (as
// videocall/WhatsApp/files already are), NEVER for adding an eighth type or
// loosening an existing one with one more generic field. Adding an eighth
// type is a deliberate, reviewed edit of AllComponentTypes() and of the
// vocabulary test in vocabulary_test.go — never an incidental addition.
package sdui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// ComponentType identifies one of the 7 closed types of the SDUI vocabulary.
type ComponentType string

// The 7 types of the vocabulary — and only these 7. No generic
// container/layout type is allowed; see the package comment.
const (
	ComponentTypeForm               ComponentType = "form"
	ComponentTypeTable              ComponentType = "table"
	ComponentTypeList               ComponentType = "list"
	ComponentTypeDetail             ComponentType = "detail"
	ComponentTypeAction             ComponentType = "action"
	ComponentTypeChart              ComponentType = "chart"
	ComponentTypeConfirmDestructive ComponentType = "confirm_destructive"
)

// AllComponentTypes returns the 7 types of the vocabulary, in a fixed order.
// Tested literally (len == 7) in vocabulary_test.go: adding an eighth type
// requires editing that test on purpose, never by accident.
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

// ErrUnknownComponentType is returned by UnmarshalScreen when the JSON
// contains a "type" outside the closed vocabulary. On the server side that is
// always an error — the server must never be asked to interpret a type it
// does not know. Tolerance for an unknown type is the Kotlin CLIENT's
// responsibility; that asymmetry is deliberate: the server is authoritative
// about what exists in the vocabulary, the client is tolerant of what does
// not exist yet in its own version.
var ErrUnknownComponentType = errors.New("sdui: unknown component type")

// ComponentBase is the base shared by every component of the vocabulary.
//
//   - Type identifies which of the 7 types this component is (the JSON discriminator).
//   - ID is stable within the screen — used for optimistic patching after a mutation.
//   - PermissionHint is purely cosmetic (dim/disable on the client); the server has
//     already omitted from the payload anything the user genuinely cannot see or
//     do. It is never the security gate — that lives entirely on the server side,
//     in the filter that assembles the screen per role.
//   - Critical governs what an old client does when it meets a "type" it does
//     not know: false (the default) = ignore it and render the rest normally;
//     true = show an "atualize o app" placeholder in the component's position.
type ComponentBase struct {
	Type           ComponentType `json:"type"`
	ID             string        `json:"id"`
	PermissionHint string        `json:"permission_hint,omitempty"`
	Critical       bool          `json:"critical,omitempty"`
}

// Base returns the ComponentBase itself — promoted by embedding into each of
// the 7 concrete types, satisfying half of the Component interface.
func (b ComponentBase) Base() ComponentBase { return b }

// Component is implemented by each of the 7 concrete types of the vocabulary.
// No type beyond the 7 defined in this file can satisfy this interface with a
// "type" outside AllComponentTypes() without going through the deliberate
// edit described in the package comment.
type Component interface {
	ComponentType() ComponentType
	Base() ComponentBase
}

// DataSource is where a component fetches its data from — always an endpoint
// already resolved by the server, never a URL the client assembles out of
// data (the same rule as ActionDescriptor in action.go).
type DataSource struct {
	Endpoint string `json:"endpoint"`
	Method   string `json:"method,omitempty"` // omitted = GET
}

// TableColumn describes one column of a TableComponent.
type TableColumn struct {
	Key      string            `json:"key"`
	Label    string            `json:"label"`
	Kind     string            `json:"kind"` // "text" | "badge" | "datetime" | ...
	BadgeMap map[string]string `json:"badge_map,omitempty"`
}

// FormField describes one field of a FormComponent.
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

// DetailField describes one field of a DetailComponent.
type DetailField struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Kind  string `json:"kind,omitempty"`
}

// ActionRef references an ActionDescriptor by id — used in
// TableComponent.RowActions and FormComponent.SubmitAction. A component never
// carries the whole action descriptor inline; the resolution (method,
// endpoint, permission) lives in ActionDescriptor (action.go).
type ActionRef struct {
	ActionID string `json:"action_id"`
	Label    string `json:"label,omitempty"`
	Style    string `json:"style,omitempty"`
}

// EmptyState is the text shown when a TableComponent/ListComponent has no
// rows.
type EmptyState struct {
	Text string `json:"text"`
}

// TableComponent is the workhorse of the administrative lists (containers,
// jobs, queue items, Jira issues).
type TableComponent struct {
	ComponentBase
	Columns    []TableColumn `json:"columns"`
	RowsSource DataSource    `json:"rows_source"`
	RowActions []ActionRef   `json:"row_actions,omitempty"`
	EmptyState *EmptyState   `json:"empty_state,omitempty"`
}

// ComponentType satisfies Component.
func (TableComponent) ComponentType() ComponentType { return ComponentTypeTable }

// ListComponent is lighter than TableComponent, for card-style feeds (the
// notifications inbox, the WhatsApp conversation list).
type ListComponent struct {
	ComponentBase
	ItemTemplate string     `json:"item_template"`
	RowsSource   DataSource `json:"rows_source"`
}

// ComponentType satisfies Component.
func (ListComponent) ComponentType() ComponentType { return ComponentTypeList }

// DetailComponent is a read-only key-value view of a single resource (a
// container's inspect, a Jira issue's fields).
type DetailComponent struct {
	ComponentBase
	DataSource DataSource    `json:"data_source"`
	Fields     []DetailField `json:"fields"`
}

// ComponentType satisfies Component.
func (DetailComponent) ComponentType() ComponentType { return ComponentTypeDetail }

// FormComponent is the workhorse of mutation input (create a container, edit
// a scheduler job, configure a notification rule).
type FormComponent struct {
	ComponentBase
	Fields       []FormField `json:"fields"`
	SubmitAction ActionRef   `json:"submit_action"`
}

// ComponentType satisfies Component.
func (FormComponent) ComponentType() ComponentType { return ComponentTypeForm }

// ActionComponent is a standalone button/menu entry, not bound to a row
// (e.g. "Deploy agora", "Reiniciar serviço").
type ActionComponent struct {
	ComponentBase
	Label    string `json:"label"`
	ActionID string `json:"action_id"`
	Style    string `json:"style,omitempty"` // "primary" | "secondary" | ...
}

// ComponentType satisfies Component.
func (ActionComponent) ComponentType() ComponentType { return ComponentTypeAction }

// ChartComponent is a read-only time series, deliberately minimal (line/bar
// only — no arbitrary vega-lite-style spec, which would be the generic-layout
// trap itself).
type ChartComponent struct {
	ComponentBase
	ChartKind    string     `json:"chart_kind"` // "line" | "bar"
	SeriesSource DataSource `json:"series_source"`
	XKey         string     `json:"x_key"`
	YKey         string     `json:"y_key"`
}

// ComponentType satisfies Component.
func (ChartComponent) ComponentType() ComponentType { return ComponentTypeChart }

// ConfirmDestructiveComponent wraps any action that deletes/kills/restarts;
// it forces a native confirmation UI on the client, optionally demanding
// typed text. This component renders nothing on its own — it is indexed by
// ActionID and consulted by the renderer when that specific action is fired
// (see the all-components.json fixture, which ties this component to a
// TableComponent row_action to document exactly that link).
type ConfirmDestructiveComponent struct {
	ComponentBase
	ActionID                 string `json:"action_id"`
	Message                  string `json:"message"`
	RequireTypedConfirmation string `json:"require_typed_confirmation,omitempty"`
}

// ComponentType satisfies Component.
func (ConfirmDestructiveComponent) ComponentType() ComponentType {
	return ComponentTypeConfirmDestructive
}

// Screen is one SDUI screen: a stable id, a title and the ordered list of
// components that make it up. Deliberately flat — Components is a list, never
// a tree, because no component may nest another component (see the package
// comment and the anti-nesting test in vocabulary_test.go).
type Screen struct {
	ID         string
	Title      string
	Components []Component
}

// Envelope is the top-level payload internal/mobilebff returns for every SDUI
// screen.
//
//	{"sdui_version": 1, "screen": {"id": "scheduler.jobs", "title": "Scheduler", "components": [...]}}
//
// SDUIVersion is only incremented on a breaking change to the ENVELOPE
// itself (rare — adding a field is not breaking). It is never incremented
// when a new component type is added; that case is covered by the client's
// tolerance for an unknown type, which is the whole point of SDUI.
type Envelope struct {
	SDUIVersion int    `json:"sdui_version"`
	Screen      Screen `json:"screen"`
}

// screenWire is Screen's wire shape — Components as json.RawMessage because
// Component is an interface and needs manual dispatch both for marshalling
// (MarshalJSON) and for unmarshalling (UnmarshalScreen).
type screenWire struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	Components []json.RawMessage `json:"components"`
}

// envelopeWire mirrors Envelope with Screen swapped for the wire shape, so
// that json.Marshal(Envelope) and UnmarshalScreen agree byte for byte on the
// order of the envelope's fields.
type envelopeWire struct {
	SDUIVersion int        `json:"sdui_version"`
	Screen      screenWire `json:"screen"`
}

// MarshalJSON serializes each concrete component directly — the embedded Type
// field (ComponentBase.Type) is the discriminator UnmarshalScreen uses to
// dispatch back to the right concrete type. Before serializing, it demands
// that Base().Type match ComponentType(): a component built with the wrong
// discriminator is a construction bug, not a valid payload — better to fail
// here than to emit an envelope the server itself cannot read back.
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

// UnmarshalScreen decodes a complete Envelope from JSON, with
// DisallowUnknownFields enabled throughout the tree — including inside each
// concrete component, once the "type" is identified. An unknown "type" is
// ErrUnknownComponentType: on the server side, a type it does not know how to
// interpret is always an error (see ErrUnknownComponentType).
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

// unmarshalComponent dispatches to the right concrete struct, with
// DisallowUnknownFields — proof, on the Go side, that a fixture carries no
// field outside the contract (the "unknown-*" fixtures exercise exactly the
// ErrUnknownComponentType path and are handled separately, see contract_test.go).
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
