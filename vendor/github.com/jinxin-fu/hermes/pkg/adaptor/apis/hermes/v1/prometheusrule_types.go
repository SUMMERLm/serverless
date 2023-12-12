/**
 * Created with IntelliJ goland.
 * @Auther: jinxin
 * @Date: 2022/05/17/17:55
 * @Description:
 */
package v1

import v1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"

func init() {
	PromRuleSchemeBuilder.Register(&v1.PrometheusRule{}, &v1.PrometheusRuleList{})
}
