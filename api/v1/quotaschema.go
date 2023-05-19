package v1

import (
	quotav1 "github.com/SUMMERLm/quota/api/v1"
)

func init() {
	SchemeBuilder.Register(&quotav1.Quota{}, &quotav1.QuotaList{})
}
