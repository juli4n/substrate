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
	"os"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

var debugCapacityzCmd = &cobra.Command{
	Use:   "debug-capacityz",
	Short: "Dump the ate-apiserver's in-memory capacity view for all nodes and worker pools",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
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

		podName, err := ateAPIPod(ctx, k8s)
		if err != nil {
			return err
		}

		data, err := portForwardFetch(ctx, restCfg, k8s, "ate-system", podName, "/capacityz")
		if err != nil {
			return fmt.Errorf("fetching capacityz from ate-apiserver: %w", err)
		}

		os.Stdout.Write(data)
		return nil
	},
}

// ateAPIPod finds any running ate-api-server pod in ate-system.
func ateAPIPod(ctx context.Context, k8s kubernetes.Interface) (string, error) {
	pods, err := k8s.CoreV1().Pods("ate-system").List(ctx, metav1.ListOptions{
		LabelSelector: "app=ate-api-server",
	})
	if err != nil {
		return "", fmt.Errorf("listing ate-api-server pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return "", fmt.Errorf("no ate-api-server pod found in ate-system")
	}
	return pods.Items[0].Name, nil
}

func init() {
	adminCmd.AddCommand(debugCapacityzCmd)
}
