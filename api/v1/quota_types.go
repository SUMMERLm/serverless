package v1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// QuotaGroupVersion is group version used to register these objects
	QuotaGroupVersion = schema.GroupVersion{Group: "serverless.pml.com.cn", Version: "v1"}

	// PromRuleSchemeBuilder is used to add go types to the GroupVersionKind scheme
	QuotaSchemeBuilder = &scheme.Builder{GroupVersion: QuotaGroupVersion}

	// PromRuleAddToScheme adds the types in this group-version to the given scheme.
	QuotaAddToScheme = QuotaSchemeBuilder.AddToScheme
)
