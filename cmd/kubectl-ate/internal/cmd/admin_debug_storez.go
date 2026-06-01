//  Copyright 2026 Google LLC
//
//  Licensed under the Apache License, Version 2.0 (the "License");
//  you may not use this file except in compliance with the License.
//  You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
//  Unless required by applicable law or agreed to in writing, software
//  distributed under the License is distributed on an "AS IS" BASIS,
//  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//  See the License for the specific language governing permissions and
//  limitations under the License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

var debugStorezCmd = &cobra.Command{
	Use:   "debug-storez <node-name>",
	Short: "Dump the bbolt worker store from the atelet on the given node",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		nodeName := args[0]
		ctx := cmd.Context()

		rules := clientcmd.NewDefaultClientConfigLoadingRules()
		if kubeconfig != "" {
			rules.ExplicitPath = kubeconfig
		}
		overrides := &clientcmd.ConfigOverrides{}
		if k8sContext != "" {
			overrides.CurrentContext = k8sContext
		}
		restCfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
		if err != nil {
			return fmt.Errorf("loading kubeconfig: %w", err)
		}

		k8s, err := kubernetes.NewForConfig(restCfg)
		if err != nil {
			return fmt.Errorf("creating k8s client: %w", err)
		}

		podName, err := ateletPodForNode(ctx, k8s, nodeName)
		if err != nil {
			return err
		}

		data, err := portForwardFetch(ctx, restCfg, k8s, "ate-system", podName, "/debug/storez")
		if err != nil {
			return fmt.Errorf("fetching storez from atelet on node %q: %w", nodeName, err)
		}

		os.Stdout.Write(data)
		return nil
	},
}

// portForwardFetch opens a port-forward tunnel through the apiserver/kubelet to
// the given pod and makes a single GET request. This works even when the
// apiserver cannot route directly to the pod IP (e.g. kind, network policies).
func portForwardFetch(ctx context.Context, restCfg *rest.Config, k8s kubernetes.Interface, namespace, podName, path string) ([]byte, error) {
	// Grab an available local port.
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, fmt.Errorf("finding free local port: %w", err)
	}
	localPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	pfURL := k8s.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(podName).
		SubResource("portforward").
		URL()

	transport, upgrader, err := spdy.RoundTripperFor(restCfg)
	if err != nil {
		return nil, fmt.Errorf("creating SPDY transport: %w", err)
	}
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, pfURL)

	stopCh := make(chan struct{})
	defer close(stopCh)
	readyCh := make(chan struct{})

	fw, err := portforward.New(dialer, []string{fmt.Sprintf("%d:9090", localPort)}, stopCh, readyCh, io.Discard, io.Discard)
	if err != nil {
		return nil, fmt.Errorf("creating port-forwarder: %w", err)
	}

	fwErrCh := make(chan error, 1)
	go func() { fwErrCh <- fw.ForwardPorts() }()

	select {
	case <-readyCh:
	case err := <-fwErrCh:
		return nil, fmt.Errorf("port-forward: %w", err)
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	resp, err := http.Get(fmt.Sprintf("http://localhost:%d%s", localPort, path))
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}
	return io.ReadAll(resp.Body)
}

// ateletPodForNode finds the atelet pod running on the given node in ate-system.
func ateletPodForNode(ctx context.Context, k8s kubernetes.Interface, nodeName string) (string, error) {
	pods, err := k8s.CoreV1().Pods("ate-system").List(ctx, metav1.ListOptions{
		LabelSelector: "app=atelet",
		FieldSelector: "spec.nodeName=" + nodeName,
	})
	if err != nil {
		return "", fmt.Errorf("listing atelet pods on node %q: %w", nodeName, err)
	}
	if len(pods.Items) == 0 {
		return "", fmt.Errorf("no atelet pod found on node %q", nodeName)
	}
	return pods.Items[0].Name, nil
}

func init() {
	adminCmd.AddCommand(debugStorezCmd)
}
