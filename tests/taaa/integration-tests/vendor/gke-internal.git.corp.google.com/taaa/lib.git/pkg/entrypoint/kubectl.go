package entrypoint

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/magefile/mage/sh"
	"gke-internal.git.corp.google.com/taaa/protobufs.git/k8sbase"
	api "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"
)

func GetClientSet(kubeconfig string) (*kubernetes.Clientset, error) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return clientset, nil
}

// TODO: use kubectl wait API instead
// https://github.com/kubernetes/kubectl/blob/ac49920c0ccb0dd0899d5300fc43713ee2dfcdc9/pkg/cmd/wait/wait_test.go#L956
func WaitForRunningPods(clientset *kubernetes.Clientset, timeout time.Duration, namespace string) error {
	ready := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	loop := true
	podNotReady := make(map[string]bool)
	go func() {
		// If client-go/kubernetes/fake also responds to ctx.Done() signals, then this "for loop" can be removed
		// to ensure that the go thread ends
		for loop {
			// You can write unit tests using client-go/kubernetes/fake
			podList, err := clientset.CoreV1().Pods(namespace).List(ctx, metaV1.ListOptions{})
			if err != nil {
				ready <- err
			}

			allReady := true
			for _, pod := range podList.Items {
				// Note: this needs a bit of an explanation. The conditions of a pod is
				// a history of timestamps of conditions the pod was in. However this is
				// not sorted. Even worse, this has only second-level granularity, so
				// theoretically two states can have the same lastTransitionTime timestamp
				var latestTime time.Time
				latestCondition := api.PodScheduled
				for _, condition := range pod.Status.Conditions {
					if condition.LastTransitionTime.Time.After(latestTime) && condition.Status == api.ConditionTrue {
						latestTime = condition.LastTransitionTime.Time
						latestCondition = condition.Type
					} else if condition.LastTransitionTime.Time.Equal(latestTime) && condition.Status == api.ConditionTrue && condition.Type == api.PodReady {
						latestCondition = condition.Type
					}
				}
				if latestCondition != api.PodReady && pod.Status.Phase != api.PodSucceeded {
					podNotReady[pod.Name] = true
					allReady = false
				} else {
					podNotReady[pod.Name] = false
				}
			}
			if allReady {
				log.Printf("All pods in namespace %q are ready", namespace)
				ready <- nil
				return
			}
			time.Sleep(1 * time.Second)
		}
		ready <- nil
	}()

	select {
	case err := <-ready:
		return err
	case <-ctx.Done():
		loop = false
		// wait for go routine to terminate
		<-ready
		// This is a bit weird since this is what the bash code does, print out all YAML and logs of pods
		// and containers. But there is so much text here that debugging using it is not that useful...
		// In the future, we should introduce special logic here to make the output remotely useful, especially
		// since for some reason the JSON->YAML marshal into text does not omitempty (creating even more garbage text).
		podList, err := clientset.CoreV1().Pods(namespace).List(context.TODO(), metaV1.ListOptions{})
		if err != nil {
			return fmt.Errorf("cannot get final pods state in namespace %q after timeout %.2fs, got error %v", namespace, timeout.Seconds(), err)
		}
		b, err := yaml.Marshal(podList)
		if err != nil {
			return fmt.Errorf("cannot unmarshal final pods state in namespace %q after timeout %.2fs, got error %v", namespace, timeout.Seconds(), err)
		}

		log.Printf("%s", string(b))

		for _, pod := range podList.Items {
			for _, container := range pod.Status.ContainerStatuses {
				podLogReq := clientset.CoreV1().Pods(namespace).GetLogs(pod.Name, &api.PodLogOptions{Container: container.Name})
				podLogs, err := podLogReq.Stream(context.TODO())
				if err != nil {
					return fmt.Errorf("cannot get logstream for pod %q in namespace %q after timeout %.2fs, got error %v", pod.Name, namespace, timeout.Seconds(), err)
				}
				defer podLogs.Close()

				buf := new(bytes.Buffer)
				_, err = io.Copy(buf, podLogs)
				if err != nil {
					return fmt.Errorf("cannot copy logs for pod %q in namespace %q after timeout %.2fs, got error %v", pod.Name, namespace, timeout.Seconds(), err)
				}
				log.Printf("Pod %q, container %q:\n%s", pod.Name, container.Name, buf.String())
			}
		}
		notReadyPods := []string{}
		for pod, notReady := range podNotReady {
			if notReady {
				notReadyPods = append(notReadyPods, pod)
			}
		}
		return fmt.Errorf("waiting for pod(s) %v in namespace %q to start...timed out %.2fs", notReadyPods, namespace, timeout.Seconds())
	}
}

// TODO: use kubectl wait API instead
// https://github.com/kubernetes/kubectl/blob/ac49920c0ccb0dd0899d5300fc43713ee2dfcdc9/pkg/cmd/wait/wait_test.go#L956
func WaitForServiceExternalIP(clientset *kubernetes.Clientset, timeout time.Duration, namespace, service string) error {
	ready := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	loop := true
	go func() {
		// If client-go/kubernetes/fake also responds to ctx.Done() signals, then this "for loop" can be removed
		// to ensure that the go thread ends
		for loop {
			service, err := clientset.CoreV1().Services(namespace).Get(ctx, service, metaV1.GetOptions{})
			if err != nil {
				ready <- err
			}

			if service.Status.LoadBalancer.Ingress[0].IP != "" {
				log.Printf("service %q in namespace %q has IP address %q", service.Name, namespace, service.Status.LoadBalancer.Ingress[0].IP)
				ready <- err
				return
			}

			if service.Status.LoadBalancer.Ingress[0].Hostname != "" {
				log.Printf("service %q in namespace %q has hostname %q", service.Name, namespace, service.Status.LoadBalancer.Ingress[0].Hostname)
				ready <- err
				return
			}
		}
		ready <- nil
	}()

	select {
	case err := <-ready:
		return err
	case <-ctx.Done():
		loop = false
		<-ready
		return fmt.Errorf("waiting for service %q in namespace %q to obtain IP/hostname...timed out %.2fs", service, namespace, timeout.Seconds())
	}
}

// GetKubeConfig creates a kubeconfig file in /tmp/ from the credentials provided.
// cloud must be installed and available via command line.
// Returns the path to the created kubeconfig file and nil on success.
// On error, if the kubeconfig command is run, then the string is the stdErr of it.
// Otherwise returned string is empty on error.
func GetKubeConfig(cred *k8sbase.CredentialRequirements) (string, error) {
	cluster, project := cred.GetCluster(), cred.GetProject()
	log.Println("Setting up kubeconfig for cluster: ", cluster)
	if cluster == "" || project == "" {
		return "", fmt.Errorf("cluster or project is blank; c: %q; p: %q", cluster, project)
	}
	kubeconfigFile, err := os.CreateTemp("/tmp/", fmt.Sprintf("taaa.kubeconfigFile.%s.*.yaml", cluster))
	if err != nil {
		return "", fmt.Errorf("Failed to create kubeconfig file for %s, got error %s", cluster, err)
	}
	ret := kubeconfigFile.Name()
	// Don't want to have an open file handle to a file about to be overwritten.
	kubeconfigFile.Close()
	log.Println("Created kubeconfig file: ", ret)
	// Determine args.
	var locationFlag string
	if cred.GetRegion() == "" {
		locationFlag = "--zone=" + cred.GetZone()
	} else {
		locationFlag = "--region=" + cred.GetRegion()
	}
	getCredsArgs := []string{
		"container", "clusters", "get-credentials", cluster,
		"--project", project,
		locationFlag,
	}
	// File environment.
	env := make(map[string]string)
	if cred.EndpointOverride != "" {
		env["CLOUDSDK_API_ENDPOINT_OVERRIDES_CONTAINER"] = cred.EndpointOverride
	}
	// Setting this to our new file will cause gcloud to write the new kubectl config to it.
	env["KUBECONFIG"] = ret
	log.Println("Running get creds cmd: gcloud ", strings.Join(getCredsArgs, " "))
	errOutput := new(strings.Builder)
	_, err = sh.Exec(env, io.Discard, errOutput, "gcloud", getCredsArgs...)
	if err != nil {
		return errOutput.String(), err
	}
	b, _ := os.ReadFile(ret)
	log.Println(
		"Created kubeconfig file from creds:\n",
		"------------------\n",
		string(b),
		"\n------------------")
	return ret, nil
}
