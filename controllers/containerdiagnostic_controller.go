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

package controllers

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	diagnosticv1 "github.com/kgibm/containerdiagoperator/api/v1"
)

// ContainerDiagnosticReconciler reconciles a ContainerDiagnostic object
type ContainerDiagnosticReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=diagnostic.ibm.com,resources=containerdiagnostics,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=diagnostic.ibm.com,resources=containerdiagnostics/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=diagnostic.ibm.com,resources=containerdiagnostics/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// Compare the state specified by
// the ContainerDiagnostic object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *ContainerDiagnosticReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	log.Info("Reconciling ContainerDiagnostic")

	containerDiagnostic := &diagnosticv1.ContainerDiagnostic{}
	err := r.Get(ctx, req.NamespacedName, containerDiagnostic)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			log.Info("ContainerDiagnostic resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get ContainerDiagnostic")
		return ctrl.Result{}, err
	}

	log.Info(fmt.Sprintf("ContainerDiagnostic command: %s", containerDiagnostic.Spec.Command))

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ContainerDiagnosticReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&diagnosticv1.ContainerDiagnostic{}).
		Complete(r)
}
