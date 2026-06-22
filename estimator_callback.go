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
