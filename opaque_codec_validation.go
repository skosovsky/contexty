package contexty

import "bytes"

// validateOpaqueMessageCodec checks lossless registered host decoding without
// pretending that a standalone message contains its full dependency context.
func validateOpaqueMessageCodec(message Message, registry *ExtensionRegistry) error {
	for _, extension := range message.Extensions {
		state, ok := opaqueStateFromExtension(extension)
		if !ok {
			if !nilInterfaceValue(extension) && extension.ExtensionType() == OpaqueStateExtensionType {
				return ErrInvalidOpaqueState
			}
			continue
		}
		if err := state.validate(); err != nil {
			return err
		}
		if state.Placement.AfterPart >= len(message.Parts) {
			return ErrInvalidOpaqueState
		}
		if err := validateOpaquePayloadCodec(state, registry); err != nil {
			return err
		}
		if err := validateOpaquePayloadRoundTrip(state.Payload, registry); err != nil {
			return err
		}
	}
	return nil
}

func validateOpaquePayloadRoundTrip(payload Extension, registry *ExtensionRegistry) error {
	original, err := EncodeExtension(payload)
	if err != nil {
		return err
	}
	restored, err := registry.Decode(original)
	if err != nil {
		return err
	}
	encoded, err := EncodeExtension(restored)
	if err != nil {
		return err
	}
	if !bytes.Equal(original, encoded) {
		return ErrMissingOpaqueStateCodec
	}
	return nil
}
