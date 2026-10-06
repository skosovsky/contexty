package contexty

// estimatorCallbackInput owns host arguments. The intrinsic stateless character
// counter is read-only and has no host callbacks, so it may borrow working data.
// Do not infer this capability from descriptors, reported quality or delegation.
func estimatorCallbackInput(estimator TokenEstimator, messages []Message) []Message {
	if _, intrinsic := estimator.(CharTokenEstimator); intrinsic {
		return messages
	}
	return cloneMessageSlice(messages)
}

func validateBuiltinEstimator(estimator TokenEstimator) error {
	switch value := estimator.(type) {
	case *FixedEstimator:
		if value == nil || value.TokensPerMessage < 0 || value.TokensPerContentPart < 0 || value.TokensPerToolCall < 0 {
			return ErrInconsistentEstimate
		}
	case *CharFallbackEstimator:
		if value == nil || value.CharsPerToken <= 0 {
			return ErrInvalidCharsPerToken
		}
		if value.TokensPerNonTextPart < 0 {
			return ErrInconsistentEstimate
		}
	case *EstimateReporter:
		if value == nil || nilInterfaceValue(value.estimator) {
			return ErrInvalidBudgetRequest
		}
		return validateBuiltinEstimator(value.estimator)
	}
	return nil
}
