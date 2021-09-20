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
	"bufio"
	"bytes"
	"context"
	"fmt"
	"github.com/go-logr/logr"
	"os"
	"os/exec"
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

	"github.com/google/uuid"
	"path/filepath"
)

const OperatorVersion = "0.80.20210920"

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
	visited   int
	successes int
}

// +kubebuilder:rbac:groups=diagnostic.ibm.com,resources=containerdiagnostics,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=diagnostic.ibm.com,resources=containerdiagnostics/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=diagnostic.ibm.com,resources=containerdiagnostics/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments/status,verbs=get
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=pods/status,verbs=get
// +kubebuilder:rbac:groups=core,resources=pods/exec,verbs=create
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

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

	logger.Info(fmt.Sprintf("Details of the ContainerDiagnostic: %+v", containerDiagnostic))

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

func (r *ContainerDiagnosticReconciler) SetStatus(status StatusEnum, message string, containerDiagnostic *diagnosticv1.ContainerDiagnostic, logger logr.Logger) {
	r.RecordEventInfo(fmt.Sprintf("Status update (%s): %s @ %s", status.ToString(), message, CurrentTimeAsString()), containerDiagnostic, logger)
	if IsInitialStatus(containerDiagnostic) {
		containerDiagnostic.Status.StatusCode = int(status)
		containerDiagnostic.Status.StatusMessage = status.ToString()
		containerDiagnostic.Status.Result = message
	} else {
		containerDiagnostic.Status.StatusCode = int(StatusMixed)
		containerDiagnostic.Status.StatusMessage = StatusMixed.ToString()
		containerDiagnostic.Status.Result = "Mixed results; describe and review Events"
	}
}

func IsInitialStatus(containerDiagnostic *diagnosticv1.ContainerDiagnostic) bool {
	if strings.HasPrefix(containerDiagnostic.Status.Result, ResultProcessing) {
		return true
	} else {
		return false
	}
}

func CurrentTimeAsString() string {
	return time.Now().Format("2006-01-02T15:04:05.000")
}

func (r *ContainerDiagnosticReconciler) ProcessResult(result ctrl.Result, err error, ctx context.Context, containerDiagnostic *diagnosticv1.ContainerDiagnostic, logger logr.Logger) (ctrl.Result, error) {
	if err == nil {
		r.RecordEventInfo(fmt.Sprintf("Finished reconciling successfully @ %s", CurrentTimeAsString()), containerDiagnostic, logger)
	} else {
		r.SetStatus(StatusError, fmt.Sprintf("Error: %s", err.Error()), containerDiagnostic, logger)
		r.RecordEventWarning(err, fmt.Sprintf("Finished reconciling with error %v @ %s", err, CurrentTimeAsString()), containerDiagnostic, logger)
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
	// https://pkg.go.dev/k8s.io/client-go/tools/record#EventRecorder
	r.EventRecorder.Event(containerDiagnostic, corev1.EventTypeWarning, "Warning", message)
}

func (r *ContainerDiagnosticReconciler) CommandVersion(ctx context.Context, req ctrl.Request, containerDiagnostic *diagnosticv1.ContainerDiagnostic, logger logr.Logger) (ctrl.Result, error) {
	logger.Info("Processing command: version")

	r.SetStatus(StatusSuccess, fmt.Sprintf("Version %s", OperatorVersion), containerDiagnostic, logger)

	return ctrl.Result{}, nil
}

func (r *ContainerDiagnosticReconciler) CommandScript(ctx context.Context, req ctrl.Request, containerDiagnostic *diagnosticv1.ContainerDiagnostic, logger logr.Logger) (ctrl.Result, error) {
	logger.Info("Processing command: script")

	if len(containerDiagnostic.Spec.Steps) == 0 {
		r.SetStatus(StatusError, fmt.Sprintf("You must specify an array of steps to perform for the script command"), containerDiagnostic, logger)
		return ctrl.Result{}, nil
	}

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
					r.SetStatus(StatusError, fmt.Sprintf("Pod not found: name: %s namespace: %s", targetObject.Name, targetObject.Namespace), containerDiagnostic, logger)
				} else {
					logger.Error(err, "Failed to get targetObject")
					return ctrl.Result{}, err
				}
			}
		}
	}

	if resultsTracker.visited > 0 {
		if resultsTracker.successes > 0 {
			var containerText string
			if resultsTracker.successes == 1 {
				containerText = "container"
			} else {
				containerText = "containers"
			}

			r.SetStatus(StatusSuccess, fmt.Sprintf("Successfully finished on %d %s", resultsTracker.successes, containerText), containerDiagnostic, logger)
		}
	} else {
		// If none were visited and there's already an error, then just leave that as probably a pod wasn't found
		if IsInitialStatus(containerDiagnostic) {
			r.SetStatus(StatusError, "No pods/containers specified", containerDiagnostic, logger)
		}
	}

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

	resultsTracker.visited++

	uuid := uuid.New().String()

	logger.Info(fmt.Sprintf("RunScriptOnContainer UUID = %s", uuid))

	// First create a local scratchspace
	localScratchSpaceDirectory := filepath.Join("/tmp/", uuid)
	err := os.MkdirAll(localScratchSpaceDirectory, os.ModePerm)
	if err != nil {
		r.SetStatus(StatusError, fmt.Sprintf("Could not create local scratchspace in %s: %+v", localScratchSpaceDirectory, err), containerDiagnostic, logger)

		// We don't stop processing other pods/containers, just return. If this is the
		// only error, status will show as error; othewrise, as mixed
		return
	}

	logger.Info(fmt.Sprintf("RunScriptOnContainer Created local scratch space: %s", localScratchSpaceDirectory))

	containerTmpFilesPrefix, ok := r.EnsureDirectoriesOnContainer(ctx, req, containerDiagnostic, logger, pod, container, resultsTracker, uuid)
	if !ok {
		// The error will have been logged within the function.
		// We don't stop processing other pods/containers, just return. If this is the
		// only error, status will show as error; othewrise, as mixed
		Cleanup(logger, localScratchSpaceDirectory)
		return
	}

	// Now loop through the steps to figure out all the files we'll need to upload
	localTarFile := filepath.Join(localScratchSpaceDirectory, "files.tar")

	filesToTar := make(map[string]bool)

	var tarArguments []string = []string{"-cv", "--dereference", "-f", localTarFile}

	for _, step := range containerDiagnostic.Spec.Steps {
		if step.Command == "install" {
			for _, command := range step.Arguments {

				fullCommand := "/usr/bin/" + command

				logger.Info(fmt.Sprintf("RunScriptOnContainer Processing install command: %s", fullCommand))

				filesToTar[fullCommand] = true

				lines, ok := r.FindSharedLibraries(logger, containerDiagnostic, fullCommand)
				if !ok {
					// The error will have been logged within the function.
					// We don't stop processing other pods/containers, just return. If this is the
					// only error, status will show as error; othewrise, as mixed
					Cleanup(logger, localScratchSpaceDirectory)
					return
				}

				for _, line := range lines {
					logger.V(2).Info(fmt.Sprintf("RunScriptOnContainer ldd file: %v", line))
					filesToTar[line] = true
				}
			}
		}
	}

	// Upload any files that are needed
	if len(filesToTar) > 0 {
		for key := range filesToTar {
			tarArguments = append(tarArguments, key)
		}

		logger.Info(fmt.Sprintf("RunScriptOnContainer creating local tar..."))

		outputBytes, err := r.ExecuteLocalCommand(logger, containerDiagnostic, "tar", tarArguments...)
		if err != nil {
			// The error will have been logged within the function.
			// We don't stop processing other pods/containers, just return. If this is the
			// only error, status will show as error; othewrise, as mixed
			Cleanup(logger, localScratchSpaceDirectory)
			return
		}

		var outputStr string = string(outputBytes[:])
		logger.V(2).Info(fmt.Sprintf("RunScriptOnContainer local tar output: %v", outputStr))

		file, err := os.Open(localTarFile)
		if err != nil {
			r.SetStatus(StatusError, fmt.Sprintf("Error reading tar file %s error: %+v", localTarFile, err), containerDiagnostic, logger)

			// We don't stop processing other pods/containers, just return. If this is the
			// only error, status will show as error; othewrise, as mixed
			Cleanup(logger, localScratchSpaceDirectory)
			return
		}

		fileReader := bufio.NewReader(file)
		logger.Info(fmt.Sprintf("RunScriptOnContainer local tar file binary size: %d", fileReader.Size()))

		var tarStdout, tarStderr bytes.Buffer
		err = r.ExecInContainer(pod, container, []string{"tar", "-xmf", "-", "-C", containerTmpFilesPrefix}, &tarStdout, &tarStderr, fileReader)

		file.Close()

		if err != nil {
			r.SetStatus(StatusError, fmt.Sprintf("Error uploading tar file to pod: %s container: %s error: %+v", pod.Name, container.Name, err), containerDiagnostic, logger)

			// We don't stop processing other pods/containers, just return. If this is the
			// only error, status will show as error; othewrise, as mixed
			Cleanup(logger, localScratchSpaceDirectory)
			return
		}

		logger.V(2).Info(fmt.Sprintf("RunScriptOnContainer tar results: stdout: %v stderr: %v", tarStdout.String(), tarStderr.String()))
	}

	// Now finally go through all the steps
	for _, step := range containerDiagnostic.Spec.Steps {
		if step.Command == "uninstall" {

			logger.Info(fmt.Sprintf("RunScriptOnContainer running uninstall step"))

			var stdout, stderr bytes.Buffer
			err := r.ExecInContainer(pod, container, []string{"rm", "-rf", containerTmpFilesPrefix}, &stdout, &stderr, nil)

			if err != nil {
				r.SetStatus(StatusError, fmt.Sprintf("Error running uninstall step on pod: %s container: %s error: %+v", pod.Name, container.Name, err), containerDiagnostic, logger)

				// We don't stop processing other pods/containers, just return. If this is the
				// only error, status will show as error; othewrise, as mixed
				Cleanup(logger, localScratchSpaceDirectory)
				return
			}

			logger.Info(fmt.Sprintf("RunScriptOnContainer finished uninstall step"))
		}
	}

	resultsTracker.successes++

	Cleanup(logger, localScratchSpaceDirectory)
}

func Cleanup(logger logr.Logger, localScratchSpaceDirectory string) {
	err := os.RemoveAll(localScratchSpaceDirectory)
	if err != nil {
		logger.Info(fmt.Sprintf("Could not cleanup %s: %+v", localScratchSpaceDirectory, err))
	}
}

func (r *ContainerDiagnosticReconciler) EnsureDirectoriesOnContainer(ctx context.Context, req ctrl.Request, containerDiagnostic *diagnosticv1.ContainerDiagnostic, logger logr.Logger, pod *corev1.Pod, container corev1.Container, resultsTracker *ResultsTracker, uuid string) (response string, ok bool) {

	containerTmpFilesPrefix := containerDiagnostic.Spec.Directory

	if containerDiagnostic.Spec.UseUUID {
		containerTmpFilesPrefix += uuid + "/"
	}

	logger.V(1).Info(fmt.Sprintf("RunScriptOnContainer running mkdir: %s", containerTmpFilesPrefix))

	var stdout, stderr bytes.Buffer
	err := r.ExecInContainer(pod, container, []string{"mkdir", "-p", containerTmpFilesPrefix}, &stdout, &stderr, nil)

	if err != nil {
		r.SetStatus(StatusError, fmt.Sprintf("Error executing mkdir in container: %+v", err), containerDiagnostic, logger)

		// We don't stop processing other pods/containers, just return. If this is the
		// only error, status will show as error; othewrise, as mixed
		return "", false
	}

	logger.V(2).Info(fmt.Sprintf("RunScriptOnContainer results: stdout: %v stderr: %v", stdout.String(), stderr.String()))

	return containerTmpFilesPrefix, true
}

func (r *ContainerDiagnosticReconciler) ExecuteLocalCommand(logger logr.Logger, containerDiagnostic *diagnosticv1.ContainerDiagnostic, command string, arguments ...string) (output []byte, err error) {

	logger.V(2).Info(fmt.Sprintf("RunScriptOnContainer ExecuteLocalCommand: %v", command))

	outputBytes, err := exec.Command(command, arguments...).CombinedOutput()
	if err != nil {
		r.SetStatus(StatusError, fmt.Sprintf("Error executing %v: %+v", command, err), containerDiagnostic, logger)

		// We don't stop processing other pods/containers, just return. If this is the
		// only error, status will show as error; othewrise, as mixed
		return nil, err
	}

	logger.V(2).Info(fmt.Sprintf("RunScriptOnContainer ExecuteLocalCommand results: %v", output))

	return outputBytes, nil
}

func (r *ContainerDiagnosticReconciler) FindSharedLibraries(logger logr.Logger, containerDiagnostic *diagnosticv1.ContainerDiagnostic, command string) ([]string, bool) {
	outputBytes, err := r.ExecuteLocalCommand(logger, containerDiagnostic, "ldd", command)
	if err != nil {
		// We don't stop processing other pods/containers, just return. If this is the
		// only error, status will show as error; othewrise, as mixed
		return nil, false
	}

	var lines []string
	scanner := bufio.NewScanner(bytes.NewReader(outputBytes))
	for scanner.Scan() {
		var line string = strings.TrimSpace(scanner.Text())
		if strings.Contains(line, "=>") {
			var pieces []string = strings.Split(line, " ")
			lines = append(lines, pieces[2])
		} else if strings.Contains(line, "ld-linux") {
			var pieces []string = strings.Split(line, " ")
			lines = append(lines, pieces[0])
		}
	}

	return lines, true
}

func (r *ContainerDiagnosticReconciler) ExecInContainer(pod *corev1.Pod, container corev1.Container, command []string, stdout *bytes.Buffer, stderr *bytes.Buffer, stdin *bufio.Reader) error {
	clientset, err := kubernetes.NewForConfig(r.Config)
	if err != nil {
		return err
	}

	restRequest := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("exec")

	if stdin == nil {
		restRequest.VersionedParams(&corev1.PodExecOptions{
			Command:   command,
			Container: container.Name,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)
	} else {
		restRequest.VersionedParams(&corev1.PodExecOptions{
			Command:   command,
			Container: container.Name,
			Stdout:    true,
			Stderr:    true,
			Stdin:     true,
			TTY:       false,
		}, scheme.ParameterCodec)
	}

	exec, err := remotecommand.NewSPDYExecutor(r.Config, "POST", restRequest.URL())
	if err != nil {
		return err
	}

	if stdin == nil {
		err = exec.Stream(remotecommand.StreamOptions{
			Stdout: stdout,
			Stderr: stderr,
			Tty:    false,
		})
	} else {
		err = exec.Stream(remotecommand.StreamOptions{
			Stdout: stdout,
			Stderr: stderr,
			Stdin:  stdin,
			Tty:    false,
		})
	}

	return err
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
