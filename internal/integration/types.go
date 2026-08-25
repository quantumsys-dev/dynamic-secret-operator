/*
Copyright 2026 QuantumSys.

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

package integration

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupName is the Group name for Argo CD resources.
const GroupName = "argoproj.io"

// SchemeGroupVersion is group version used to register Argo CD objects.
var SchemeGroupVersion = schema.GroupVersion{Group: GroupName, Version: "v1alpha1"}

var (
	// SchemeBuilder registers Argo CD types into a runtime.Scheme.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)
	// AddToScheme applies all stored functions to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&Application{},
		&ApplicationList{},
	)
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}

// ResourceIgnoreDifferences contains resource filter and list of json paths which should be ignored during diff with live state.
type ResourceIgnoreDifferences struct {
	Group             string   `json:"group,omitempty"`
	Kind              string   `json:"kind,omitempty"`
	Name              string   `json:"name,omitempty"`
	Namespace         string   `json:"namespace,omitempty"`
	JSONPointers      []string `json:"jsonPointers,omitempty"`
	JQPathExpressions []string `json:"jqPathExpressions,omitempty"`
}

// ApplicationSpec represents desired application state.
type ApplicationSpec struct {
	Project           string                      `json:"project,omitempty"`
	IgnoreDifferences []ResourceIgnoreDifferences `json:"ignoreDifferences,omitempty"`
}

// +kubebuilder:object:root=true

// Application is a definition of Application resource.
type Application struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ApplicationSpec `json:"spec,omitempty"`
}

// DeepCopyInto copies all properties of this object into another object of the same type.
func (in *Application) DeepCopyInto(out *Application) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
}

// DeepCopy copies the receiver, creating a new Application.
func (in *Application) DeepCopy() *Application {
	if in == nil {
		return nil
	}
	out := new(Application)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject returns a generically typed copy of an object.
func (in *Application) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopyInto copies all properties of this object into another object of the same type.
func (in *ApplicationSpec) DeepCopyInto(out *ApplicationSpec) {
	*out = *in
	if in.IgnoreDifferences != nil {
		in, out := &in.IgnoreDifferences, &out.IgnoreDifferences
		*out = make([]ResourceIgnoreDifferences, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopyInto copies all properties of this object into another object of the same type.
func (in *ResourceIgnoreDifferences) DeepCopyInto(out *ResourceIgnoreDifferences) {
	*out = *in
	if in.JSONPointers != nil {
		in, out := &in.JSONPointers, &out.JSONPointers
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	if in.JQPathExpressions != nil {
		in, out := &in.JQPathExpressions, &out.JQPathExpressions
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// +kubebuilder:object:root=true

// ApplicationList contains a list of Application.
type ApplicationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Application `json:"items"`
}

// DeepCopyInto copies all properties of this object into another object of the same type.
func (in *ApplicationList) DeepCopyInto(out *ApplicationList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]Application, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy copies the receiver, creating a new ApplicationList.
func (in *ApplicationList) DeepCopy() *ApplicationList {
	if in == nil {
		return nil
	}
	out := new(ApplicationList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject returns a generically typed copy of an object.
func (in *ApplicationList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}
