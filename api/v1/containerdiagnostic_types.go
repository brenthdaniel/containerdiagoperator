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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ContainerDiagnosticStep struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=install;execute;package;uninstall
	Command string `json:"command"`

	// +kubebuilder:validation:Optional
	Arguments []string `json:"arguments"`
}

// ContainerDiagnosticSpec defines the desired state of ContainerDiagnostic
type ContainerDiagnosticSpec struct {

	// Command is one of: version, script
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=version;script
	Command string `json:"command,omitempty"`

	// Optional. Arguments for the specified Command.
	// +kubebuilder:validation:Optional
	Arguments []string `json:"arguments"`

	// Optional. A list of ObjectReferences.
	// See https://kubernetes.io/docs/reference/kubernetes-api/common-definitions/object-reference/
	// +kubebuilder:validation:Optional
	TargetObjects []corev1.ObjectReference `json:"targetObjects"`

	// A list of steps to perform for the specified Command.
	// +kubebuilder:validation:Optional
	Steps []ContainerDiagnosticStep `json:"steps"`

	// Optional. Target directory for diagnostic files. Must end in trailing slash.
	// Defaults to /tmp/containerdiag/.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default="/tmp/containerdiag/"
	Directory string `json:"directory,omitempty"`

	// Optional. Whether or not to use a unique identifier in the directory
	// name of each execution. Defaults to true.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=true
	UseUUID bool `json:"useuuid,omitempty"`
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

	// +kubebuilder:validation:Optional
	Download string `json:"download"`
}

// ContainerDiagnostic is the Schema for the containerdiagnostics API
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Command",type=string,JSONPath=`.spec.command`
// +kubebuilder:printcolumn:name="StatusMessage",type=string,JSONPath=`.status.statusMessage`
// +kubebuilder:printcolumn:name="Result",type=string,JSONPath=`.status.result`
// +kubebuilder:printcolumn:name="Download",type=string,JSONPath=`.status.download`
type ContainerDiagnostic struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ContainerDiagnosticSpec   `json:"spec,omitempty"`
	Status ContainerDiagnosticStatus `json:"status,omitempty"`
}

// ContainerDiagnosticList contains a list of ContainerDiagnostic
// +kubebuilder:object:root=true
type ContainerDiagnosticList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ContainerDiagnostic `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ContainerDiagnostic{}, &ContainerDiagnosticList{})
}
