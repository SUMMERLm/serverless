package v1

import hermesv1 "github.com/jinxin-fu/hermes/pkg/adaptor/apis/hermes/v1"

func init() {
	SubScriberRuleSchemeBuilder.Register(&hermesv1.SubscriberRule{}, &hermesv1.SubscriberRuleList{})
}
