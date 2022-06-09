package v1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// SubScriberRuleGroupVersion is group version used to register these objects
	SubScriberRuleGroupVersion = schema.GroupVersion{Group: "hermes.pml.com", Version: "v1"}

	// PromRuleSchemeBuilder is used to add go types to the GroupVersionKind scheme
	SubScriberRuleSchemeBuilder = &scheme.Builder{GroupVersion: SubScriberRuleGroupVersion}

	// PromRuleAddToScheme adds the types in this group-version to the given scheme.
	SubScriberRuleAddToScheme = SubScriberRuleSchemeBuilder.AddToScheme
)
