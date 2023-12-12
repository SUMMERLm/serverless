/**
 * Created with IntelliJ goland.
 * @Auther: jinxin
 * @Date: 2022/05/17/17:38
 * @Description:
 */
package v1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// PromRuleGroupVersion is group version used to register these objects
	PromRuleGroupVersion = schema.GroupVersion{Group: "monitoring.coreos.com", Version: "v1"}

	// PromRuleSchemeBuilder is used to add go types to the GroupVersionKind scheme
	PromRuleSchemeBuilder = &scheme.Builder{GroupVersion: PromRuleGroupVersion}

	// PromRuleAddToScheme adds the types in this group-version to the given scheme.
	PromRuleAddToScheme = PromRuleSchemeBuilder.AddToScheme
)
