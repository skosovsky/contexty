package contexty

import "encoding/json"

// ResourceConfiguration pins every configured interpretation, including unused
// codecs. It contains no registry handles, authorization scopes or executions.
type ResourceConfiguration struct {
	Materialization Descriptor         `json:"materialization"`
	Reader          Descriptor         `json:"reader"`
	Projection      Descriptor         `json:"projection"`
	Labels          Descriptor         `json:"labels"`
	Estimate        EstimateProfile    `json:"estimate"`
	Trace           TraceConfiguration `json:"trace"`
}

func (c ResourceConfiguration) Clone() ResourceConfiguration {
	c.Estimate = c.Estimate.clone()
	c.Trace = c.Trace.clone()
	return c
}

func (c ResourceConfiguration) Validate() error {
	for _, descriptor := range []Descriptor{c.Reader, c.Projection, c.Labels, c.Materialization} {
		if err := descriptor.Validate(); err != nil {
			return err
		}
	}
	if c.Trace.RequireOrigins || c.Trace.RequireDurableIdentity {
		return ErrInvalidResource
	}
	if err := c.Estimate.validate(); err != nil {
		return err
	}
	return c.Trace.validate()
}

func (c ResourceConfiguration) Ref() (ContentRef, error) {
	if err := c.Validate(); err != nil {
		return ContentRef{}, err
	}
	wire, err := json.Marshal(c)
	if err != nil {
		return ContentRef{}, err
	}
	digest, err := canonicalJSONDigest(wire)
	if err != nil {
		return ContentRef{}, err
	}
	return ContentRef{ID: "resource/configuration", Digest: digest, Occurrence: ""}, nil
}

func resourceMetadataIdentity() Descriptor {
	return Descriptor{ID: "contexty/resource-source-metadata", Revision: resourceIntrinsicRevision}
}

func (r ResourceResolver) Configuration() (ResourceConfiguration, error) {
	if r.Reporter == nil || nilInterfaceValue(r.Reporter.estimator) {
		return ResourceConfiguration{}, ErrInvalidEstimateReport
	}
	if err := validateMaterializationPolicy(r.Materialization); err != nil {
		return ResourceConfiguration{}, err
	}
	identity := r.LabelPolicyIdentity
	if nilInterfaceValue(r.Labels.Policy) {
		if r.Labels.Policy != nil || identity != (Descriptor{ID: "", Revision: ""}) ||
			len(r.Labels.RequiredTypes) != 0 {
			return ResourceConfiguration{}, ErrMissingLabelPolicy
		}
		identity = resourceMetadataIdentity()
	} else if identity.Validate() != nil || identity.ID == resourceMetadataIdentity().ID {
		return ResourceConfiguration{}, ErrInvalidResource
	}
	var profile TraceProfile
	profile.Codec = snapshotJSONSerializer(r.Reporter.codec)
	profile.Labels = r.Labels
	profile.Labels.Registry = r.Labels.Registry.snapshot()
	profile.Labels.RequiredTypes = canonicalLabelTypes(r.Labels.RequiredTypes)
	profile.Codecs = cloneCodecBindings(r.Codecs)
	trace, err := profile.configuration(false)
	if err != nil {
		return ResourceConfiguration{}, err
	}
	configuration := ResourceConfiguration{
		Materialization: r.Materialization.Identity,
		Reader:          r.ReaderIdentity,
		Projection:      r.ProjectionIdentity,
		Labels:          identity,
		Estimate:        r.Reporter.profile.clone(),
		Trace:           trace,
	}
	if err = configuration.Validate(); err != nil {
		return ResourceConfiguration{}, err
	}
	return configuration, nil
}
