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
	"io"
	"io/ioutil"
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

const OperatorVersion = "0.134.20210927"

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

type ContextTracker struct {
	visited                 int
	successes               int
	localPermanentDirectory string
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

	logger.Info("ContainerDiagnosticReconciler Reconcile called")

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
		r.RecordEventInfo(fmt.Sprintf("Finished reconciling @ %s", CurrentTimeAsString()), containerDiagnostic, logger)
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

	// Create a permanent directory for this run
	// user is 'nobody' so /tmp is really the only place
	uuid := uuid.New().String()
	localPermanentDirectory := filepath.Join("/tmp/containerdiagoutput", uuid)
	err := os.MkdirAll(localPermanentDirectory, os.ModePerm)
	if err != nil {
		r.SetStatus(StatusError, fmt.Sprintf("Could not create local permanent output space in %s: %+v", localPermanentDirectory, err), containerDiagnostic, logger)
		return ctrl.Result{}, err
	}

	contextTracker := ContextTracker{localPermanentDirectory: localPermanentDirectory}

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
				r.RunScriptOnPod(ctx, req, containerDiagnostic, logger, pod, &contextTracker)
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

	logger.Info("CommandScript: walking " + localPermanentDirectory)

	// Next, let's walk our perm dir and unzip anything in place
	var zips []string = []string{}

	err = filepath.Walk(localPermanentDirectory,
		func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if strings.HasSuffix(info.Name(), "zip") {
				// path is the absolute path since we call Walk with an absolute path
				// https://pkg.go.dev/path/filepath#WalkFunc
				zips = append(zips, path)
			}
			return nil
		})
	if err != nil {
		r.SetStatus(StatusError, fmt.Sprintf("Could not walk local permanent output space in %s: %+v", localPermanentDirectory, err), containerDiagnostic, logger)
		return ctrl.Result{}, err
	}

	logger.Info("CommandScript: processing zips")

	for _, zipFile := range zips {
		logger.Info(fmt.Sprintf("Unzipping %s", zipFile))

		outputBytes, err := r.ExecuteLocalCommand(logger, containerDiagnostic, "unzip", zipFile, "-d", filepath.Dir(zipFile))
		var outputStr string = string(outputBytes[:])
		if err != nil {
			r.SetStatus(StatusError, fmt.Sprintf("Could not unzip %s: %+v %s", zipFile, err, outputStr), containerDiagnostic, logger)
			return ctrl.Result{}, err
		}

		logger.V(1).Info(fmt.Sprintf("CommandScript unzip output: %v", outputStr))

		os.Remove(zipFile)
	}

	logger.Info("CommandScript: creating final zip")

	// Finally, zip up the files for final user download
	finalZip := filepath.Join("/tmp/containerdiagoutput", fmt.Sprintf("containerdiag_%s_%s.zip", time.Now().Format("20060102_150405"), uuid))
	outputBytes, err := r.ExecuteLocalCommand(logger, containerDiagnostic, "sh", "-c", fmt.Sprintf("cd %s; zip -r %s %s", filepath.Dir(localPermanentDirectory), finalZip, filepath.Base(localPermanentDirectory)))
	var outputStr string = string(outputBytes[:])
	if err != nil {
		r.SetStatus(StatusError, fmt.Sprintf("Could not zip %s: %+v %s", finalZip, err, outputStr), containerDiagnostic, logger)
		return ctrl.Result{}, err
	}

	logger.V(1).Info(fmt.Sprintf("CommandScript zip output: %v", outputStr))

	// Now that we've created the zip, we can delete the actual directory to save space
	os.RemoveAll(localPermanentDirectory)

	// Container name
	hostnameBytes, err := ioutil.ReadFile("/etc/hostname")
	if err != nil {
		r.SetStatus(StatusError, fmt.Sprintf("Could not read /etc/hostname: %+v", err), containerDiagnostic, logger)
		return ctrl.Result{}, err
	}

	containerName := string(hostnameBytes)
	containerName = strings.ReplaceAll(containerName, "\n", "")
	containerName = strings.ReplaceAll(containerName, "\r", "")

	containerDiagnostic.Status.Download = fmt.Sprintf("kubectl cp %s:%s %s --container=manager --namespace=containerdiagoperator-system", containerName, finalZip, filepath.Base(finalZip))

	if contextTracker.visited > 0 {
		if contextTracker.successes > 0 {
			var containerText string
			if contextTracker.successes == 1 {
				containerText = "container"
			} else {
				containerText = "containers"
			}

			r.SetStatus(StatusSuccess, fmt.Sprintf("Successfully finished on %d %s", contextTracker.successes, containerText), containerDiagnostic, logger)
		}
	} else {
		// If none were visited and there's already an error, then just leave that as probably a pod wasn't found
		if IsInitialStatus(containerDiagnostic) {
			r.SetStatus(StatusError, "No pods/containers specified", containerDiagnostic, logger)
		}
	}

	return ctrl.Result{}, nil
}

func (r *ContainerDiagnosticReconciler) RunScriptOnPod(ctx context.Context, req ctrl.Request, containerDiagnostic *diagnosticv1.ContainerDiagnostic, logger logr.Logger, pod *corev1.Pod, contextTracker *ContextTracker) {
	logger.Info(fmt.Sprintf("RunScriptOnPod containers: %d", len(pod.Spec.Containers)))
	for _, container := range pod.Spec.Containers {
		logger.Info(fmt.Sprintf("RunScriptOnPod container: %+v", container))
		r.RunScriptOnContainer(ctx, req, containerDiagnostic, logger, pod, container, contextTracker)
	}
}

func (r *ContainerDiagnosticReconciler) RunScriptOnContainer(ctx context.Context, req ctrl.Request, containerDiagnostic *diagnosticv1.ContainerDiagnostic, logger logr.Logger, pod *corev1.Pod, container corev1.Container, contextTracker *ContextTracker) {
	logger.Info(fmt.Sprintf("RunScriptOnContainer pod: %s, container: %s", pod.Name, container.Name))

	contextTracker.visited++

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

	containerTmpFilesPrefix, ok := r.EnsureDirectoriesOnContainer(ctx, req, containerDiagnostic, logger, pod, container, contextTracker, uuid)
	if !ok {
		// The error will have been logged within the above function.
		// We don't stop processing other pods/containers, just return. If this is the
		// only error, status will show as error; othewrise, as mixed
		Cleanup(logger, localScratchSpaceDirectory)
		return
	}

	// Now loop through the steps to figure out all the files we'll need to upload
	localTarFile := filepath.Join(localScratchSpaceDirectory, "files.tar")

	filesToTar := make(map[string]bool)

	// "--dereference" not needed because we tar up the symlink targets too
	var tarArguments []string = []string{"-cv", "-f", localTarFile}

	// First add in some basic commands that we'll always need
	for _, command := range []string{
		"/usr/bin/cp",
		"/usr/bin/date",
		"/usr/bin/echo",
		"/usr/bin/pwd",
		"/usr/bin/tee",
		"/usr/bin/rm",
		"/usr/bin/sleep",
		"/usr/bin/zip",
	} {
		ok := r.ProcessInstallCommand(command, filesToTar, containerDiagnostic, logger)
		if !ok {
			// The error will have been logged within the above function.
			// We don't stop processing other pods/containers, just return. If this is the
			// only error, status will show as error; othewrise, as mixed
			Cleanup(logger, localScratchSpaceDirectory)
			return
		}
	}

	// Now add in any commands that the user has specified
	for _, step := range containerDiagnostic.Spec.Steps {
		if step.Command == "install" {
			for _, commandLine := range step.Arguments {
				for _, command := range strings.Split(commandLine, " ") {

					// Specifically known and pre-packaged scripts
					if command == "linperf.sh" {
						// Add prereqs that aren't already installed above
						for _, command := range []string{
							"/usr/bin/whoami",
							"/usr/bin/netstat",
							"/usr/bin/top",
							"/usr/bin/expr",
							"/usr/bin/vmstat",
							"/usr/bin/ps",
							"/usr/bin/kill",
							"/usr/bin/dmesg",
							"/usr/bin/df",
							"/usr/bin/gzip",
							"/usr/bin/tput",
						} {
							ok := r.ProcessInstallCommand(command, filesToTar, containerDiagnostic, logger)
							if !ok {
								// The error will have been logged within the above function.
								// We don't stop processing other pods/containers, just return. If this is the
								// only error, status will show as error; othewrise, as mixed
								Cleanup(logger, localScratchSpaceDirectory)
								return
							}
						}

						// Now we need to copy the script over to our local scratch space to modify the command executions
						localScript := filepath.Join(localScratchSpaceDirectory, command)
						localScriptFile, err := os.OpenFile(localScript, os.O_CREATE|os.O_WRONLY, os.ModePerm)

						if err != nil {
							r.SetStatus(StatusError, fmt.Sprintf("Error writing local script file %s error: %+v", command, err), containerDiagnostic, logger)

							// We don't stop processing other pods/containers, just return. If this is the
							// only error, status will show as error; othewrise, as mixed
							Cleanup(logger, localScratchSpaceDirectory)
							return
						}

						localScriptFileWriter := bufio.NewWriter(localScriptFile)

						sourceScript := "/usr/local/bin/" + command
						sourceScriptFile, err := os.Open(sourceScript)
						if err != nil {
							r.SetStatus(StatusError, fmt.Sprintf("Error reading file %s error: %+v", sourceScript, err), containerDiagnostic, logger)

							// We don't stop processing other pods/containers, just return. If this is the
							// only error, status will show as error; othewrise, as mixed
							Cleanup(logger, localScratchSpaceDirectory)
							return
						}

						sourceScriptFileScanner := bufio.NewScanner(sourceScriptFile)
						for sourceScriptFileScanner.Scan() {
							line := sourceScriptFileScanner.Text()
							localScriptFileWriter.WriteString(line + "\n")
						}

						sourceScriptFile.Close()

						localScriptFileWriter.Flush()
						localScriptFile.Close()

						os.Chmod(localScript, os.ModePerm)

						// Now that the local file is written, add it to the files to transfer over:
						filesToTar[localScript] = true

					} else {

						// Normal command from /usr/bin/

						ok := r.ProcessInstallCommand("/usr/bin/"+command, filesToTar, containerDiagnostic, logger)
						if !ok {
							// The error will have been logged within the above function.
							// We don't stop processing other pods/containers, just return. If this is the
							// only error, status will show as error; othewrise, as mixed
							Cleanup(logger, localScratchSpaceDirectory)
							return
						}
					}
				}
			}
		}
	}

	remoteFilesToPackage := make(map[string]bool)

	// Create the execute script(s)
	for stepIndex, step := range containerDiagnostic.Spec.Steps {
		if step.Command == "execute" {

			if step.Arguments == nil || len(step.Arguments) == 0 {
				r.SetStatus(StatusError, fmt.Sprintf("Run command must have arguments including the binary name"), containerDiagnostic, logger)

				// We don't stop processing other pods/containers, just return. If this is the
				// only error, status will show as error; othewrise, as mixed
				Cleanup(logger, localScratchSpaceDirectory)
				return
			}

			remoteOutputFile := filepath.Join(containerTmpFilesPrefix, fmt.Sprintf("containerdiag_%s_%d.txt", time.Now().Format("20060102_150405"), (stepIndex+1)))

			remoteFilesToPackage[remoteOutputFile] = true

			localExecuteScript := filepath.Join(localScratchSpaceDirectory, fmt.Sprintf("execute_%d.sh", (stepIndex+1)))

			localExecuteFile, err := os.OpenFile(localExecuteScript, os.O_CREATE|os.O_WRONLY, os.ModePerm)

			if err != nil {
				r.SetStatus(StatusError, fmt.Sprintf("Error writing local execute.sh file %s error: %+v", localExecuteScript, err), containerDiagnostic, logger)

				// We don't stop processing other pods/containers, just return. If this is the
				// only error, status will show as error; othewrise, as mixed
				Cleanup(logger, localScratchSpaceDirectory)
				return
			}

			localExecuteFileWriter := bufio.NewWriter(localExecuteFile)

			// Script header
			localExecuteFileWriter.WriteString("#!/bin/sh\n")

			// Change directory to the temp directory in case any command needs to use the current working directory for scratch files
			localExecuteFileWriter.WriteString(fmt.Sprintf("cd %s\n", containerTmpFilesPrefix))

			// Echo outputfile directly to stdout without redirecting to the output file because a user executing this script wants to know where the output goes
			WriteExecutionLine(localExecuteFileWriter, containerTmpFilesPrefix, "echo", fmt.Sprintf("\"Writing output to %s\"", remoteOutputFile), false, remoteOutputFile)

			// Echo a simple prolog to the output file
			WriteExecutionLine(localExecuteFileWriter, containerTmpFilesPrefix, "date", "", true, remoteOutputFile)
			WriteExecutionLine(localExecuteFileWriter, containerTmpFilesPrefix, "echo", "\"containerdiag: Started execution\"", true, remoteOutputFile)
			WriteExecutionLine(localExecuteFileWriter, containerTmpFilesPrefix, "echo", "\"\"", true, remoteOutputFile)

			// Build the command execution with arguments
			command := step.Arguments[0]
			arguments := ""

			spaceIndex := strings.Index(command, " ")
			if spaceIndex != -1 {
				arguments = command[spaceIndex+1:]
				command = command[:spaceIndex]
			}

			for index, arg := range step.Arguments {
				if index > 0 {
					if len(arguments) > 0 {
						arguments += " "
					}
					arguments += arg
				}
			}

			// Execute the command with arguments
			WriteExecutionLine(localExecuteFileWriter, containerTmpFilesPrefix, command, arguments, true, remoteOutputFile)

			// Echo a simple epilog to the output file
			WriteExecutionLine(localExecuteFileWriter, containerTmpFilesPrefix, "echo", "\"\"", true, remoteOutputFile)
			WriteExecutionLine(localExecuteFileWriter, containerTmpFilesPrefix, "date", "", true, remoteOutputFile)
			WriteExecutionLine(localExecuteFileWriter, containerTmpFilesPrefix, "echo", "\"containerdiag: Finished execution\"", true, remoteOutputFile)

			localExecuteFileWriter.Flush()
			localExecuteFile.Close()

			os.Chmod(localExecuteScript, os.ModePerm)

			// Now that the local file is written, add it to the files to transfer over:
			filesToTar[localExecuteScript] = true
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
			// The error will have been logged within the above function.
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
		err = r.ExecInContainer(pod, container, []string{"tar", "-xmf", "-", "-C", containerTmpFilesPrefix}, &tarStdout, &tarStderr, fileReader, nil)

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
	for stepIndex, step := range containerDiagnostic.Spec.Steps {
		if step.Command == "execute" {

			logger.Info(fmt.Sprintf("RunScriptOnContainer running 'execute' step"))

			remoteExecutionScript := filepath.Join(containerTmpFilesPrefix, localScratchSpaceDirectory, fmt.Sprintf("execute_%d.sh", (stepIndex+1)))

			logger.Info(fmt.Sprintf("RunScriptOnContainer Running %v", remoteExecutionScript))

			var stdout, stderr bytes.Buffer
			err := r.ExecInContainer(pod, container, []string{remoteExecutionScript}, &stdout, &stderr, nil, nil)

			if err != nil {
				r.SetStatus(StatusError, fmt.Sprintf("Error running 'execute' step on pod: %s container: %s error: %+v", pod.Name, container.Name, err), containerDiagnostic, logger)

				// We don't stop processing other pods/containers, just return. If this is the
				// only error, status will show as error; othewrise, as mixed
				Cleanup(logger, localScratchSpaceDirectory)
				return
			}

			stdoutStr := stdout.String()
			stderrStr := stderr.String()
			if len(stderrStr) == 0 {
				logger.Info(fmt.Sprintf("RunScriptOnContainer stdout:\n%s\n", stdoutStr))
			} else {
				logger.Info(fmt.Sprintf("RunScriptOnContainer stdout:\n%s\n\nstderr:\n%s\n", stdoutStr, stderrStr))
			}

			logger.Info(fmt.Sprintf("RunScriptOnContainer finished 'execute' step"))
		}
	}

	// Package up files
	zipFileName := fmt.Sprintf("containerdiag_%s.zip", time.Now().Format("20060102_150405"))
	remoteZipFile := filepath.Join(containerTmpFilesPrefix, zipFileName)

	var zipStdout, zipStderr bytes.Buffer
	var zipCommand []string = strings.Split(GetExecutionCommand(containerTmpFilesPrefix, "zip", ""), " ")
	zipCommand = append(zipCommand, remoteZipFile)
	for remoteFileToPackage := range remoteFilesToPackage {
		zipCommand = append(zipCommand, remoteFileToPackage)
	}

	logger.Info(fmt.Sprintf("RunScriptOnContainer zipping up remote files: %v", zipCommand))

	err = r.ExecInContainer(pod, container, zipCommand, &zipStdout, &zipStderr, nil, nil)

	if err != nil {
		r.SetStatus(StatusError, fmt.Sprintf("Error running 'zip' step on pod: %s container: %s error: %+v", pod.Name, container.Name, err), containerDiagnostic, logger)

		// We don't stop processing other pods/containers, just return. If this is the
		// only error, status will show as error; othewrise, as mixed
		Cleanup(logger, localScratchSpaceDirectory)
		return
	}

	// Download the files locally
	localDownloadedTarFile := filepath.Join(localScratchSpaceDirectory, strings.ReplaceAll(zipFileName, ".zip", ".tar"))
	localZipFile := filepath.Join(localScratchSpaceDirectory, zipFileName)

	logger.Info(fmt.Sprintf("RunScriptOnContainer Downloading file to: %s", localDownloadedTarFile))

	file, err := os.OpenFile(localDownloadedTarFile, os.O_CREATE|os.O_WRONLY, os.ModePerm)
	if err != nil {
		r.SetStatus(StatusError, fmt.Sprintf("Error opening tar file %s error: %+v", localDownloadedTarFile, err), containerDiagnostic, logger)

		// We don't stop processing other pods/containers, just return. If this is the
		// only error, status will show as error; othewrise, as mixed
		Cleanup(logger, localScratchSpaceDirectory)
		return
	}

	fileWriter := bufio.NewWriter(file)

	var tarStderr bytes.Buffer
	args := []string{"tar", "-C", filepath.Dir(remoteZipFile), "-cf", "-", filepath.Base(remoteZipFile)}
	err = r.ExecInContainer(pod, container, args, nil, &tarStderr, nil, fileWriter)

	fileWriter.Flush()
	file.Close()

	if err != nil {
		r.SetStatus(StatusError, fmt.Sprintf("Error downloading %s from pod: %s container: %s error: %+v for %v", remoteZipFile, pod.Name, container.Name, err, args), containerDiagnostic, logger)

		// We don't stop processing other pods/containers, just return. If this is the
		// only error, status will show as error; othewrise, as mixed
		Cleanup(logger, localScratchSpaceDirectory)
		return
	}

	// Now untar the tar file which will expand the zip file
	logger.Info(fmt.Sprintf("RunScriptOnContainer Untarring downloaded file: %s", localDownloadedTarFile))

	outputBytes, err := r.ExecuteLocalCommand(logger, containerDiagnostic, "tar", "-C", localScratchSpaceDirectory, "-xvf", localDownloadedTarFile)
	var outputStr string = string(outputBytes[:])
	if err != nil {
		r.SetStatus(StatusError, fmt.Sprintf("Could not untar %s: %+v %s", localDownloadedTarFile, err, outputStr), containerDiagnostic, logger)

		// We don't stop processing other pods/containers, just return. If this is the
		// only error, status will show as error; othewrise, as mixed
		Cleanup(logger, localScratchSpaceDirectory)
		return
	}

	logger.V(1).Info(fmt.Sprintf("RunScriptOnContainer untar output: %v", outputStr))

	// Delete the tar file
	os.Remove(localDownloadedTarFile)

	fileInfo, err := os.Stat(localZipFile)
	if err != nil {
		r.SetStatus(StatusError, fmt.Sprintf("Could not find local zip file: %s error: %+v", localZipFile, err), containerDiagnostic, logger)

		// We don't stop processing other pods/containers, just return. If this is the
		// only error, status will show as error; othewrise, as mixed
		Cleanup(logger, localScratchSpaceDirectory)
		return
	}

	logger.Info(fmt.Sprintf("RunScriptOnContainer Finished downloading zip file, size: %d", fileInfo.Size()))

	// Now move the zip over to the permanent space
	permdir := filepath.Join(contextTracker.localPermanentDirectory, "namespaces", pod.Namespace, "pods", pod.Name, "containers", container.Name, uuid)
	err = os.MkdirAll(permdir, os.ModePerm)
	if err != nil {
		r.SetStatus(StatusError, fmt.Sprintf("Could not create permanent output space in %s: %+v", permdir, err), containerDiagnostic, logger)

		// We don't stop processing other pods/containers, just return. If this is the
		// only error, status will show as error; othewrise, as mixed
		Cleanup(logger, localScratchSpaceDirectory)
		return
	}

	// Finally copy the zip file over
	err = CopyFile(localZipFile, filepath.Join(permdir, zipFileName))
	if err != nil {
		r.SetStatus(StatusError, fmt.Sprintf("Could not copy file file to permanent directory %s: %+v", permdir, err), containerDiagnostic, logger)

		// We don't stop processing other pods/containers, just return. If this is the
		// only error, status will show as error; othewrise, as mixed
		Cleanup(logger, localScratchSpaceDirectory)
		return
	}

	logger.Info(fmt.Sprintf("RunScriptOnContainer Copied zip file to: %s", permdir))

	// Cleanup if requested
	for _, step := range containerDiagnostic.Spec.Steps {
		if step.Command == "uninstall" {

			logger.Info(fmt.Sprintf("RunScriptOnContainer running 'uninstall' step"))

			var stdout, stderr bytes.Buffer
			err := r.ExecInContainer(pod, container, []string{"rm", "-rf", containerTmpFilesPrefix}, &stdout, &stderr, nil, nil)

			if err != nil {
				r.SetStatus(StatusError, fmt.Sprintf("Error running uninstall step on pod: %s container: %s error: %+v", pod.Name, container.Name, err), containerDiagnostic, logger)

				// We don't stop processing other pods/containers, just return. If this is the
				// only error, status will show as error; othewrise, as mixed
				Cleanup(logger, localScratchSpaceDirectory)
				return
			}

			logger.Info(fmt.Sprintf("RunScriptOnContainer finished 'uninstall' step"))
		}
	}

	contextTracker.successes++

	Cleanup(logger, localScratchSpaceDirectory)
}

func CopyFile(src string, dest string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	outFile, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer outFile.Close()

	_, err = io.Copy(outFile, srcFile)
	return err
}

func WriteExecutionLine(fileWriter *bufio.Writer, containerTmpFilesPrefix string, command string, arguments string, redirectOutput bool, outputFile string) {
	// See https://www.kernel.org/doc/man-pages/online/pages/man8/ld-linux.so.8.html
	var redirectStr string = ""
	if redirectOutput {
		redirectStr = fmt.Sprintf(">> %s 2>&1", outputFile)
	}
	fileWriter.WriteString(fmt.Sprintf("%s %s\n", GetExecutionCommand(containerTmpFilesPrefix, command, arguments), redirectStr))
}

func GetExecutionCommand(containerTmpFilesPrefix string, command string, arguments string) string {
	// See https://www.kernel.org/doc/man-pages/online/pages/man8/ld-linux.so.8.html
	result := fmt.Sprintf("%s --inhibit-cache --library-path %s %s", filepath.Join(containerTmpFilesPrefix, "lib64", "ld-linux-x86-64.so.2"), filepath.Join(containerTmpFilesPrefix, "lib64"), filepath.Join(containerTmpFilesPrefix, "usr", "bin", command))
	if len(arguments) > 0 {
		result += " " + arguments
	}
	return result
}

func (r *ContainerDiagnosticReconciler) ProcessInstallCommand(fullCommand string, filesToTar map[string]bool, containerDiagnostic *diagnosticv1.ContainerDiagnostic, logger logr.Logger) bool {

	fullCommand = filepath.Clean(fullCommand)

	logger.V(1).Info(fmt.Sprintf("RunScriptOnContainer Processing install command: %s", fullCommand))

	filesToTar[fullCommand] = true

	lines, ok := r.FindSharedLibraries(logger, containerDiagnostic, fullCommand)
	if !ok {
		// The error will have been logged within the above function.
		return false
	}

	for _, line := range lines {
		logger.V(2).Info(fmt.Sprintf("RunScriptOnContainer ldd file: %v", line))
		filesToTar[line] = true

		// Follow any symlinks and add those
		var last string = line
		var count int = 0

		for count < 10 {
			logger.V(2).Info(fmt.Sprintf("ProcessInstallCommand checking for symlinks: %s", last))
			fileInfo, err := os.Lstat(last)
			if err == nil {
				if fileInfo.Mode()&os.ModeSymlink != 0 {
					checkLink, err := os.Readlink(last)
					logger.V(2).Info(fmt.Sprintf("ProcessInstallCommand found symlink: %s", checkLink))
					if err == nil {
						if checkLink != last {

							if !filepath.IsAbs(checkLink) {
								checkLink = filepath.Clean(filepath.Join(filepath.Dir(last), checkLink))
							} else {
								checkLink = filepath.Clean(checkLink)
							}

							logger.V(2).Info(fmt.Sprintf("ProcessInstallCommand after cleaning: %s", checkLink))

							filesToTar[checkLink] = true
							last = checkLink
						} else {
							break
						}
					} else {
						break
					}
				} else {
					break
				}
			} else {
				break
			}

			// Avoid an infinite loop
			count++
		}
	}

	return true
}

func Cleanup(logger logr.Logger, localScratchSpaceDirectory string) {
	err := os.RemoveAll(localScratchSpaceDirectory)
	if err != nil {
		logger.Info(fmt.Sprintf("Could not cleanup %s: %+v", localScratchSpaceDirectory, err))
	}
}

func (r *ContainerDiagnosticReconciler) EnsureDirectoriesOnContainer(ctx context.Context, req ctrl.Request, containerDiagnostic *diagnosticv1.ContainerDiagnostic, logger logr.Logger, pod *corev1.Pod, container corev1.Container, contextTracker *ContextTracker, uuid string) (response string, ok bool) {

	containerTmpFilesPrefix := containerDiagnostic.Spec.Directory

	if containerDiagnostic.Spec.UseUUID {
		containerTmpFilesPrefix += uuid + "/"
	}

	logger.V(1).Info(fmt.Sprintf("RunScriptOnContainer running mkdir: %s", containerTmpFilesPrefix))

	var stdout, stderr bytes.Buffer
	err := r.ExecInContainer(pod, container, []string{"mkdir", "-p", containerTmpFilesPrefix}, &stdout, &stderr, nil, nil)

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
		r.SetStatus(StatusError, fmt.Sprintf("Error executing %v %v: %+v", command, arguments, err), containerDiagnostic, logger)

		// We don't stop processing other pods/containers, just return. If this is the
		// only error, status will show as error; othewrise, as mixed
		return outputBytes, err
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

func (r *ContainerDiagnosticReconciler) ExecInContainer(pod *corev1.Pod, container corev1.Container, command []string, stdout *bytes.Buffer, stderr *bytes.Buffer, stdin *bufio.Reader, stdoutWriter *bufio.Writer) error {
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
		if stdoutWriter == nil {
			err = exec.Stream(remotecommand.StreamOptions{
				Stdout: stdout,
				Stderr: stderr,
				Tty:    false,
			})
		} else {
			err = exec.Stream(remotecommand.StreamOptions{
				Stdout: stdoutWriter,
				Stderr: stderr,
				Tty:    false,
			})
		}
	} else {
		if stdoutWriter == nil {
			err = exec.Stream(remotecommand.StreamOptions{
				Stdout: stdout,
				Stderr: stderr,
				Stdin:  stdin,
				Tty:    false,
			})
		} else {
			err = exec.Stream(remotecommand.StreamOptions{
				Stdout: stdoutWriter,
				Stderr: stderr,
				Stdin:  stdin,
				Tty:    false,
			})
		}
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
