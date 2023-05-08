/*
Copyright 2023 summerlmm.

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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type WorkloadType string

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ServerlessStatus defines the observed state of Serverless
type ServerlessStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Serverless is the Schema for the serverlesses API
type Serverless struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ServerlessSpec   `json:"spec,omitempty"`
	Status ServerlessStatus `json:"status,omitempty"`
}
type ServerlessSpec struct {
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// +required
	Name string `json:"name,omitempty"`
	// +optional
	// +kubebuilder:validation:Optional
	// +kubebuilder:pruning:PreserveUnknownFields
	Module corev1.PodTemplateSpec `json:"module" protobuf:"bytes,3,opt,name=module"`
	// +required
	RuntimeType string `json:"runtimeType,omitempty"`
	// +required
	// +kubebuilder:default=1
	Dispersion int32 `json:"dispersion,omitempty"`
	// +required
	Workload Workload `json:"workload,omitempty"`
	// +required
	SchedulePolicy SchedulePolicy `json:"schedulePolicy,omitempty"`
	// +optional
	ClusterTolerations []corev1.Toleration `json:"clusterTolerations,omitempty"`
}
type Workload struct {
	// +required
	// +kubebuilder:validation:Enum=deployment;serverless;affinitydaemon;userapp;knative
	Workloadtype WorkloadType `json:"workloadtype,omitempty"`
	// +optional
	TraitServerless *TraitServerless `json:"traitServerless,omitempty"`
}

type SchedulePolicy struct {
	// +optional
	SpecificResource *metav1.LabelSelector `json:"specificResource,omitempty"`
	// +optional
	NetEnvironment *metav1.LabelSelector `json:"netenvironment,omitempty"`
	// +optional
	GeoLocation *metav1.LabelSelector `json:"geolocation,omitempty"`
	// +optional
	Provider *metav1.LabelSelector `json:"provider,omitempty"`
}

/*
{
"maxReplicas": 100,
"maxQPS": 10000,
"foundingmember": true,
"threshhold": "minCPU:10,...."
"qpsStep": 10,
"resplicasStep": 1,
}
*/
type TraitServerless struct {
	MaxReplicas    int32  `json:"maxReplicas,omitempty"`
	MaxQPS         int32  `json:"maxQPS,omitempty"`
	Threshold      string `json:"threshold,omitempty"`
	Foundingmember bool   `json:"foundingmember,omitempty"`
	QpsStep        int32  `json:"qpsStep,omitempty"`
	ResplicasStep  int32  `json:"resplicasStep,omitempty"`
}

//+kubebuilder:object:root=true

// ServerlessList contains a list of Serverless
type ServerlessList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Serverless `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Serverless{}, &ServerlessList{})
}
