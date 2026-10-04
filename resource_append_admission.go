package contexty

func validateResourceAppendAdmission(_ CompileManifest, merge *ResourceArtifactMerge) error {
	if merge != nil && !merge.Prepared {
		return ErrInvalidResource
	}
	return nil
}
