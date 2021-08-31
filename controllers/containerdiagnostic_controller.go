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
	"bytes"
	"context"
	"fmt"
	"github.com/go-logr/logr"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	diagnosticv1 "github.com/kgibm/containerdiagoperator/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/tools/remotecommand"
)

const OperatorVersion = "0.33.20210831"

const ResultProcessing = "Processing..."

type StatusEnum int

const (
	StatusUninitialized StatusEnum = iota
	StatusSuccess
	StatusError
	StatusMixed
)

var StatusEnumNames = []string{
	"uninitialized",
	"success",
	"error",
	"mixed",
}

func (se StatusEnum) ToString() string {
	return StatusEnumNames[se]
}

func (se StatusEnum) Value() int {
	return int(se)
}

// ContainerDiagnosticReconciler reconciles a ContainerDiagnostic object
type ContainerDiagnosticReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	Config        *rest.Config
	EventRecorder record.EventRecorder
}

type ResultsTracker struct {
	successes int
}

//+kubebuilder:rbac:groups=diagnostic.ibm.com,resources=containerdiagnostics,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=diagnostic.ibm.com,resources=containerdiagnostics/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=diagnostic.ibm.com,resources=containerdiagnostics/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
//+kubebuilder:rbac:groups=apps,resources=deployments/status,verbs=get
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=pods/status,verbs=get

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
	logger := log.FromContext(ctx)

	logger.Info("Reconciling ContainerDiagnostic")

	containerDiagnostic := &diagnosticv1.ContainerDiagnostic{}
	err := r.Get(ctx, req.NamespacedName, containerDiagnostic)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			logger.Info("ContainerDiagnostic resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		logger.Error(err, "Failed to get ContainerDiagnostic")
		return ctrl.Result{}, err
	}

	r.RecordEventInfo(fmt.Sprintf("Started reconciling ContainerDiagnostic name: %s, namespace: %s, command: %s, status: %s @ %s", containerDiagnostic.Name, containerDiagnostic.Namespace, containerDiagnostic.Spec.Command, StatusEnum(containerDiagnostic.Status.StatusCode).ToString(), CurrentTimeAsString()), containerDiagnostic, logger)

	logger.Info(fmt.Sprintf("Reconciling ContainerDiagnostic: %v", containerDiagnostic))

	// This is just a marker status
	containerDiagnostic.Status.Result = ResultProcessing

	var result ctrl.Result = ctrl.Result{}
	err = nil

	if containerDiagnostic.Status.StatusCode == StatusUninitialized.Value() {
		switch containerDiagnostic.Spec.Command {
		case "version":
			result, err = r.CommandVersion(ctx, req, containerDiagnostic, logger)
		case "script":
			result, err = r.CommandScript(ctx, req, containerDiagnostic, logger)
		}
	}

	return r.ProcessResult(result, err, ctx, containerDiagnostic, logger)
}

func SetStatus(status StatusEnum, message string, containerDiagnostic *diagnosticv1.ContainerDiagnostic, logger logr.Logger) {
	r.RecordEventWarning(err, fmt.Sprintf("Status update (%s): %s @ %s", status.ToString(), message, CurrentTimeAsString()), containerDiagnostic, logger)
	if strings.HasPrefix(containerDiagnostic.Status.Result, ResultProcessing) {
		containerDiagnostic.Status.StatusCode = int(status)
		containerDiagnostic.Status.StatusMessage = status.ToString()
		containerDiagnostic.Status.Result = message
	} else {
		containerDiagnostic.Status.StatusCode = int(StatusMixed)
		containerDiagnostic.Status.StatusMessage = StatusMixed.ToString()
		containerDiagnostic.Status.Result = "Mixed results; describe and review Events."
	}
}

func CurrentTimeAsString() string {
	return time.Now().Format("2006-01-02T15:04:05.000")
}

func (r *ContainerDiagnosticReconciler) ProcessResult(result ctrl.Result, err error, ctx context.Context, containerDiagnostic *diagnosticv1.ContainerDiagnostic, logger logr.Logger) (ctrl.Result, error) {
	if err == nil {
		r.RecordEventInfo(fmt.Sprintf("Finished reconciling successfully @ %s", CurrentTimeAsString()), containerDiagnostic, logger)
	} else {
		r.RecordEventWarning(err, fmt.Sprintf("Finished reconciling with error %v @ %s", err, CurrentTimeAsString()), containerDiagnostic, logger)
		SetStatus(StatusError, fmt.Sprintf("Error: %s", err.Error()), containerDiagnostic, logger)
	}

	if !strings.HasPrefix(containerDiagnostic.Status.Result, ResultProcessing) {
		statusErr := r.Status().Update(ctx, containerDiagnostic)
		if statusErr != nil {
			logger.Error(statusErr, fmt.Sprintf("Failed to update ContainerDiagnostic status: %v", statusErr))
			if err == nil {
				return ctrl.Result{}, statusErr
			} else {
				// If we're already processing an error, don't override that
				// with the status update error
			}
		}
	}

	return result, err
}

func (r *ContainerDiagnosticReconciler) RecordEventInfo(message string, containerDiagnostic *diagnosticv1.ContainerDiagnostic, logger logr.Logger) {
	logger.Info(message)

	// https://pkg.go.dev/k8s.io/client-go/tools/record#EventRecorder
	r.EventRecorder.Event(containerDiagnostic, corev1.EventTypeNormal, "Informational", message)
}

func (r *ContainerDiagnosticReconciler) RecordEventWarning(err error, message string, containerDiagnostic *diagnosticv1.ContainerDiagnostic, logger logr.Logger) {
	logger.Error(err, message)
	// k8s only has normal and warning event types
	r.EventRecorder.Event(containerDiagnostic, corev1.EventTypeWarning, "Warning", message)
}

func (r *ContainerDiagnosticReconciler) CommandVersion(ctx context.Context, req ctrl.Request, containerDiagnostic *diagnosticv1.ContainerDiagnostic, logger logr.Logger) (ctrl.Result, error) {
	logger.Info("Processing command: version")

	SetStatus(StatusSuccess, fmt.Sprintf("Version %s", OperatorVersion), containerDiagnostic, logger)

	return ctrl.Result{}, nil
}

func (r *ContainerDiagnosticReconciler) CommandScript(ctx context.Context, req ctrl.Request, containerDiagnostic *diagnosticv1.ContainerDiagnostic, logger logr.Logger) (ctrl.Result, error) {
	logger.Info("Processing command: script")

	resultsTracker := ResultsTracker{}

	if containerDiagnostic.Spec.TargetObjects != nil {
		for _, targetObject := range containerDiagnostic.Spec.TargetObjects {

			logger.Info(fmt.Sprintf("targetObject: %+v", targetObject))

			pod := &corev1.Pod{}
			err := r.Get(context.Background(), client.ObjectKey{
				Namespace: targetObject.Namespace,
				Name:      targetObject.Name,
			}, pod)

			if err == nil {
				logger.V(1).Info(fmt.Sprintf("found pod: %+v", pod))
				r.RunScriptOnPod(ctx, req, containerDiagnostic, logger, pod, &resultsTracker)
			} else {
				if errors.IsNotFound(err) {
					logger.Info("Pod not found. Ignoring since object must be deleted")
				} else {
					logger.Error(err, "Failed to get targetObject")
					return ctrl.Result{}, err
				}
			}
		}
	}

	SetStatus(StatusSuccess, fmt.Sprintf("Successfully finished on %d containers", resultsTracker.successes), containerDiagnostic, logger)

	return ctrl.Result{}, nil
}

func (r *ContainerDiagnosticReconciler) RunScriptOnPod(ctx context.Context, req ctrl.Request, containerDiagnostic *diagnosticv1.ContainerDiagnostic, logger logr.Logger, pod *corev1.Pod, resultsTracker *ResultsTracker) {
	logger.Info(fmt.Sprintf("RunScriptOnPod containers: %d", len(pod.Spec.Containers)))
	for _, container := range pod.Spec.Containers {
		logger.Info(fmt.Sprintf("RunScriptOnPod container: %+v", container))
		r.RunScriptOnContainer(ctx, req, containerDiagnostic, logger, pod, container, resultsTracker)
	}
}

func (r *ContainerDiagnosticReconciler) RunScriptOnContainer(ctx context.Context, req ctrl.Request, containerDiagnostic *diagnosticv1.ContainerDiagnostic, logger logr.Logger, pod *corev1.Pod, container corev1.Container, resultsTracker *ResultsTracker) {
	logger.Info(fmt.Sprintf("RunScriptOnContainer pod: %s, container: %s", pod.Name, container.Name))

	clientset, err := kubernetes.NewForConfig(r.Config)
	if err != nil {
		logger.Error(err, "Error creating Clientset")
	}

	restRequest := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("exec")

	restRequest.VersionedParams(&corev1.PodExecOptions{
		Command:   []string{"/bin/sh", "-c", "ls -l /"},
		Container: container.Name,
		Stdout:    true,
		Stderr:    true,
		TTY:       false,
	}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(r.Config, "POST", restRequest.URL())
	if err != nil {
	}

	var stdout, stderr bytes.Buffer
	err = exec.Stream(remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
		Tty:    false,
	})

	resultsTracker.successes++

	logger.Info(fmt.Sprintf("RunScriptOnContainer results: stdout: %v stderr: %v", stdout.String(), stderr.String()))
}

// SetupWithManager sets up the controller with the Manager.
func (r *ContainerDiagnosticReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/builder#Builder
	result := ctrl.NewControllerManagedBy(mgr).
		For(&diagnosticv1.ContainerDiagnostic{}).
		Complete(r)
	r.Config = mgr.GetConfig()
	r.EventRecorder = mgr.GetEventRecorderFor("containerdiagnostic")
	return result
}
