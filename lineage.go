package contexty

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrInvalidDescriptor  = errors.New("contexty: invalid descriptor")
	ErrInvalidContentRef  = errors.New("contexty: invalid content reference")
	ErrInvalidLineage     = errors.New("contexty: invalid lineage")
	ErrDuplicateTransform = errors.New("contexty: duplicate transform identity")
)

// Descriptor identifies host-pinned behavior or encoding, without storing execution.
// ID and Revision are opaque strings; callers own their interpretation.
type Descriptor struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}

// Validate requires an explicit identity for reproducible behavior.
func (d Descriptor) Validate() error {
	if d.ID == "" || d.Revision == "" {
		return ErrInvalidDescriptor
	}
	return nil
}

// ContentRef identifies an immutable content revision, not an access capability.
// Equal IDs with different content must have different digests.
type ContentRef struct {
	ID         string `json:"id"`
	Digest     string `json:"digest"`
	Occurrence string `json:"occurrence,omitempty"`
}

// Validate rejects missing identities or noncanonical SHA-256 digests.
func (r ContentRef) Validate() error {
	b, err := hex.DecodeString(r.Digest)
	if r.ID == "" || err != nil || len(b) != sha256.Size || hex.EncodeToString(b) != r.Digest {
		return ErrInvalidContentRef
	}
	return nil
}

// MessageContentRef hashes the complete typed wire message, including extensions.
// Canonicalization preserves array order and numeric literals, sorts object keys,
// and ignores JSON whitespace. Codec identity must be pinned separately by callers.
func MessageContentRef(msg Message, codec JSONSerializer) (ContentRef, error) {
	if msg.ID == "" {
		return ContentRef{}, ErrInvalidContentRef
	}
	wire, err := codec.Marshal(msg)
	if err != nil {
		return ContentRef{}, err
	}
	digest, err := canonicalJSONDigest(wire)
	if err != nil {
		return ContentRef{}, err
	}
	return ContentRef{ID: msg.ID, Digest: digest, Occurrence: ""}, nil
}

func canonicalJSONDigest(wire []byte) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(wire))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("contexty: canonical content: %w", err)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("contexty: canonical content: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

// LineageRecord describes one transform invocation. A descriptor can be reused,
// but each invocation has a unique ID. Input content is referenced, never embedded.
type LineageRecord struct {
	ID          string       `json:"id"`
	Transform   Descriptor   `json:"transform"`
	Inputs      []ContentRef `json:"inputs"`
	Outputs     []ContentRef `json:"outputs"`
	DecisionRef string       `json:"decision_ref,omitempty"`
	Stage       string       `json:"stage,omitempty"`
}

// Lineage is a normalized graph of immutable content revisions. Unresolved lists
// known references whose source records aren't available under host policy.
type Lineage struct {
	Records    []LineageRecord `json:"records"`
	Unresolved []ContentRef    `json:"unresolved,omitempty"`
}

// Clone provides a defensive copy of every graph container.
func (l Lineage) Clone() Lineage {
	out := Lineage{
		Records:    make([]LineageRecord, len(l.Records)),
		Unresolved: append([]ContentRef(nil), l.Unresolved...),
	}
	for i, record := range l.Records {
		out.Records[i] = record
		out.Records[i].Inputs = append([]ContentRef(nil), record.Inputs...)
		out.Records[i].Outputs = append([]ContentRef(nil), record.Outputs...)
	}
	return out
}

// WithRecord appends an invocation without mutating the original graph.
func (l Lineage) WithRecord(record LineageRecord) (Lineage, error) {
	next := l.Clone()
	next.Records = append(next.Records, record)
	next = next.Clone()
	if err := next.Validate(); err != nil {
		return Lineage{}, err
	}
	return next, nil
}

// Validate checks transform identity, content references, unique producers and
// acyclicity. Pass-through refs (identical input/output) do not create self-edges.
func (l Lineage) Validate() error {
	ids := make(map[string]struct{}, len(l.Records))
	producers := make(map[ContentRef]string)
	edges := make(map[ContentRef][]ContentRef)
	for _, record := range l.Records {
		if err := validateLineageRecord(record); err != nil {
			return err
		}
		if _, duplicate := ids[record.ID]; duplicate {
			return ErrDuplicateTransform
		}
		ids[record.ID] = struct{}{}
		for _, output := range record.Outputs {
			if err := addLineageOutput(edges, producers, record, output); err != nil {
				return err
			}
		}
	}
	for _, ref := range l.Unresolved {
		if err := ref.Validate(); err != nil {
			return err
		}
	}
	return validateLineageCycles(edges)
}

func validateLineageRecord(record LineageRecord) error {
	if record.ID == "" || (len(record.Inputs) == 0 && len(record.Outputs) == 0) {
		return ErrInvalidLineage
	}
	if err := record.Transform.Validate(); err != nil {
		return err
	}
	for _, refs := range [][]ContentRef{record.Inputs, record.Outputs} {
		seen := make(map[ContentRef]struct{}, len(refs))
		for _, ref := range refs {
			if err := ref.Validate(); err != nil {
				return err
			}
			if _, duplicate := seen[ref]; duplicate {
				return ErrInvalidLineage
			}
			seen[ref] = struct{}{}
		}
	}
	return nil
}

// EncodeLineage validates the graph before persisting it.
func EncodeLineage(graph Lineage) ([]byte, error) {
	if err := graph.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(graph)
}

// DecodeLineage validates persisted references and transform identities. It does
// not resolve missing source content or infer provenance for absent records.
func DecodeLineage(wire []byte) (Lineage, error) {
	var graph Lineage
	if err := json.Unmarshal(wire, &graph); err != nil {
		return Lineage{}, fmt.Errorf("contexty: lineage decode: %w", err)
	}
	if err := graph.Validate(); err != nil {
		return Lineage{}, err
	}
	return graph.Clone(), nil
}

func addLineageOutput(edges map[ContentRef][]ContentRef, producers map[ContentRef]string,
	record LineageRecord, output ContentRef,
) error {
	changed := len(record.Inputs) == 0
	for _, input := range record.Inputs {
		if input != output {
			changed = true
			edges[output] = append(edges[output], input)
		}
	}
	if changed {
		if _, duplicate := producers[output]; duplicate {
			return fmt.Errorf("%w: multiple producers of %q", ErrInvalidLineage, output.ID)
		}
		producers[output] = record.ID
	}
	return nil
}

func validateLineageCycles(edges map[ContentRef][]ContentRef) error {
	visiting := make(map[ContentRef]bool)
	visited := make(map[ContentRef]bool)
	var visit func(ContentRef) error
	visit = func(ref ContentRef) error {
		if visiting[ref] {
			return fmt.Errorf("%w: cycle at %q", ErrInvalidLineage, ref.ID)
		}
		if visited[ref] {
			return nil
		}
		visiting[ref] = true
		for _, input := range edges[ref] {
			if err := visit(input); err != nil {
				return err
			}
		}
		delete(visiting, ref)
		visited[ref] = true
		return nil
	}
	for ref := range edges {
		if err := visit(ref); err != nil {
			return err
		}
	}
	return nil
}
