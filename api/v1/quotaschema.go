package v1

import (
	quotav1 "github.com/SUMMERLm/quota/api/v1"
)

func init() {
	QuotaSchemeBuilder.Register(&quotav1.Quota{}, &quotav1.QuotaList{})
}
