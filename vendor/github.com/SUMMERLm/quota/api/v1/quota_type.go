/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// QuotaStatus defines the observed state of SubscriberRule
type QuotaStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// Quota is a specification for a Serverless Quotaresource
type Quota struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Status QuotaStatus `json:"status,omitempty"`
	Spec   QuotaSpec   `json:"spec,omitempty"`
}

// QuotaSpec is the spec for a Foo resource
type QuotaSpec struct {
	// +optional
	SupervisorName string `json:"supervisorName,omitempty"`
	// +optional
	LocalName string `json:"localName,omitempty"`
	// +optional
	NetworkRegister []NetworkRegisterSpec `json:"networkRegister,omitempty"`
	// +optional
	ChildName []string `json:"childName,omitempty"`
	// +optional
	ChildAlert []ChildAlertSpec `json:"childAlert,omitempty"`
	// +optional
	ClusterAreaType string `json:"clusterAreaType,omitempty"`
	// +optional
	PodQpsQuota []PodQpsQuotaSpec `json:"podQpsQuota,omitempty"`
	// +optional
	PodQpsReal []PodQpsQuotaRealSpec `json:"podQpsReal,omitempty"`
	// +optional
	ChildClusterState []ChildClusterState `json:"childClusterState,omitempty"`
	// +optional
	PodQpsIncreaseOrDecrease []PodQpsIncreaseOrDecreaseSpec `json:"podQpsIncreaseOrDecrease,omitempty"`
}

type NetworkRegisterSpec struct {
	// +optional
	Scnid string `json:"scnid,omitempty"`
	// +optional
	Clustername string `json:"clustername,omitempty"`
}

type ChildAlertSpec struct {
	// +optional
	ClusterName string `json:"clusterName,omitempty"`
	// +optional
	Alert bool `json:"alert,omitempty"`
}

type PodQpsQuotaSpec struct {
	// +optional
	PodName string `json:"podName,omitempty"`
	// +optional
	QpsQuota int `json:"qpsQuota,omitempty"`
	// +optional
	ClusterName string `json:"clusterName,omitempty"`
}

type PodQpsQuotaRealSpec struct {
	// +optional
	PodName string `json:"podName,omitempty"`
	// +optional
	QpsReal int `json:"qpsReal,omitempty"`
}

type PodQpsIncreaseOrDecreaseSpec struct {
	// +optional
	PodName string `json:"podName,omitempty"`
	// +optional
	QpsIncreaseOrDecrease int `json:"qpsIncreaseOrDecrease,omitempty"`
}

type ChildClusterState struct {
	// +optional
	ClusterName string `json:"clusterName,omitempty"`
	// +optional
	ClusterState string `json:"clusterState,omitempty"`
	// +optional
	Quota int `json:"quota,omitempty"`
	// +optional
	QuotaRequire int `json:"quotaRequire,omitempty"`
	// +optional
	QuotaRemain int `json:"quotaRemain,omitempty"`
}

//+kubebuilder:object:root=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// QuotaList is a list of Quota resources
type QuotaList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Quota `json:"items"`
}
