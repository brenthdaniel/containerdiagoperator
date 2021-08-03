/*
Copyright 2021.

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

// ContainerDiagnosticSpec defines the desired state of ContainerDiagnostic
type ContainerDiagnosticSpec struct {

	// Command is one of: version, listjava
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=version;listjava
	Command string `json:"command,omitempty"`

	// +kubebuilder:validation:Optional
	Arguments []string `json:"arguments"`
}

// ContainerDiagnosticStatus defines the observed state of ContainerDiagnostic
type ContainerDiagnosticStatus struct {

	// +kubebuilder:validation:Optional
	StatusCode int `json:"statusCode"`

	// +kubebuilder:validation:Optional
	StatusMessage string `json:"statusMessage"`

	// +kubebuilder:validation:Optional
	Result string `json:"result"`

	// +kubebuilder:validation:Optional
	Log string `json:"log"`
}

// ContainerDiagnostic is the Schema for the containerdiagnostics API
//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
type ContainerDiagnostic struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ContainerDiagnosticSpec   `json:"spec,omitempty"`
	Status ContainerDiagnosticStatus `json:"status,omitempty"`
}

// ContainerDiagnosticList contains a list of ContainerDiagnostic
//+kubebuilder:object:root=true
type ContainerDiagnosticList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ContainerDiagnostic `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ContainerDiagnostic{}, &ContainerDiagnosticList{})
}
