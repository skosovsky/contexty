package main

import (
	"encoding/json"
	"slices"

	"github.com/skosovsky/contexty"
)

const (
	signatureType  = "host.fixture/signature"
	compactionType = "host.fixture/opaque-compaction"
)

// signatureFixture models bytes received from a host protocol, not a generated signature.
type signatureFixture struct {
	Signature []byte `json:"signature"`
}

func (s signatureFixture) ExtensionType() string { return signatureType }

func (s signatureFixture) CloneExtension() contexty.Extension {
	return signatureFixture{Signature: slices.Clone(s.Signature)}
}

// opaqueCompactionFixture has an independent codec and remains uninterpreted.
// It is not a local text summary or a contexty.CompactionRecord.
type opaqueCompactionFixture struct {
	ItemID string `json:"item_id"`
	Opaque []byte `json:"opaque"`
}

func (c opaqueCompactionFixture) ExtensionType() string { return compactionType }

func (c opaqueCompactionFixture) CloneExtension() contexty.Extension {
	return opaqueCompactionFixture{ItemID: c.ItemID, Opaque: slices.Clone(c.Opaque)}
}

func registerPayloads(registry *contexty.ExtensionRegistry) {
	registry.RegisterOpaquePayload(
		signatureType,
		payloadCodec(signatureType),
		func(data []byte) (contexty.Extension, error) {
			var payload signatureFixture
			if err := json.Unmarshal(data, &payload); err != nil {
				return nil, err
			}
			return payload, nil
		},
	)
	registry.RegisterOpaquePayload(
		compactionType,
		payloadCodec(compactionType),
		func(data []byte) (contexty.Extension, error) {
			var payload opaqueCompactionFixture
			if err := json.Unmarshal(data, &payload); err != nil {
				return nil, err
			}
			return payload, nil
		},
	)
}

func payloadCodec(typeID string) contexty.Descriptor {
	return contexty.Descriptor{ID: typeID + "/json", Revision: "1"}
}
