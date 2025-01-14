/*


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

package main

import (
	"flag"
	"fmt"
	"runtime"

	performancev1 "github.com/openshift-kni/performance-addon-operators/api/v1"
	performancev1alpha1 "github.com/openshift-kni/performance-addon-operators/api/v1alpha1"
	performancev2 "github.com/openshift-kni/performance-addon-operators/api/v2"
	"github.com/openshift-kni/performance-addon-operators/controllers"
	"github.com/openshift-kni/performance-addon-operators/pkg/controller/performanceprofile/components"
	"github.com/openshift-kni/performance-addon-operators/pkg/utils/leaderelection"
	"github.com/openshift-kni/performance-addon-operators/version"
	"github.com/spf13/cobra"

	tunedv1 "github.com/openshift/cluster-node-tuning-operator/pkg/apis/tuned/v1"
	mcov1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/openshift-kni/performance-addon-operators/pkg/cmd/render"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	// +kubebuilder:scaffold:imports
)

const (
	leaderElectionID = "performance-addon-operators" // Autogenerated plural form
	webhookPort      = 4343
	webhookCertDir   = "/apiserver.local.config/certificates"
	webhookCertName  = "apiserver.crt"
	webhookKeyName   = "apiserver.key"
)

// Change below variables to serve metrics on different host or port.
var (
	metricsHost       = "0.0.0.0"
	metricsPort int32 = 8383
)

var (
	scheme = apiruntime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(performancev1alpha1.AddToScheme(scheme))
	utilruntime.Must(performancev1.AddToScheme(scheme))
	utilruntime.Must(performancev2.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func printVersion() {
	klog.Infof("Operator Version: %s", version.Version)
	klog.Infof("Git Commit: %s", version.GitCommit)
	klog.Infof("Build Date: %s", version.BuildDate)
	klog.Infof("Go Version: %s", runtime.Version())
	klog.Infof("Go OS/Arch: %s/%s", runtime.GOOS, runtime.GOARCH)
}

func main() {
	// Add klog flags
	klog.InitFlags(nil)

	command := newRootCommand()
	if err := command.Execute(); err != nil {
		klog.Errorf("%v", err)
	}
}

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "performance-addon-operator",
		Short: "OpenShift performance addon operator",
		Run: func(cmd *cobra.Command, args []string) {
			// if no subcommand just run the usual
			runPAO()
		},
	}

	cmd.AddCommand(render.NewRenderCommand())
	return cmd
}

func runPAO() {
	var metricsAddr string
	var enableLeaderElection bool

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	flag.StringVar(&metricsAddr, "metrics-addr", fmt.Sprintf("%s:%d", metricsHost, metricsPort),
		"The address the metric endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", true,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")

	flag.Parse()

	printVersion()

	// we have two namespaces that we need to watch
	// 1. tuned namespace - for tuned resources
	// 2. None namespace - for cluster wide resources
	namespaces := []string{
		components.NamespaceNodeTuningOperator,
		metav1.NamespaceNone,
	}

	restConfig := ctrl.GetConfigOrDie()
	le := leaderelection.GetLeaderElectionConfig(restConfig, enableLeaderElection)

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		NewCache:           cache.MultiNamespacedCacheBuilder(namespaces),
		Scheme:             scheme,
		MetricsBindAddress: metricsAddr,
		LeaderElection:     enableLeaderElection,
		LeaderElectionID:   leaderElectionID,
		LeaseDuration:      &le.LeaseDuration.Duration,
		RetryPeriod:        &le.RetryPeriod.Duration,
		RenewDeadline:      &le.RenewDeadline.Duration,
	})

	if err != nil {
		klog.Exit(err.Error())
	}

	if err := mcov1.AddToScheme(mgr.GetScheme()); err != nil {
		klog.Exit(err.Error())
	}

	if err := tunedv1.AddToScheme(mgr.GetScheme()); err != nil {
		klog.Exit(err.Error())
	}

	if err = (&controllers.PerformanceProfileReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		Recorder:  mgr.GetEventRecorderFor("performance-profile-controller"),
		AssetsDir: components.AssetsDir,
	}).SetupWithManager(mgr); err != nil {
		klog.Exitf("unable to create PerformanceProfile controller : %v", err)
	}

	// configure webhook server
	webHookServer := mgr.GetWebhookServer()
	webHookServer.Port = webhookPort
	webHookServer.CertDir = webhookCertDir
	webHookServer.CertName = webhookCertName
	webHookServer.KeyName = webhookKeyName

	if err = (&performancev1.PerformanceProfile{}).SetupWebhookWithManager(mgr); err != nil {
		klog.Exitf("unable to create PerformanceProfile v1 webhook : %v", err)
	}
	if err = (&performancev2.PerformanceProfile{}).SetupWebhookWithManager(mgr); err != nil {
		klog.Exitf("unable to create PerformanceProfile v2 webhook : %v", err)
	}

	// +kubebuilder:scaffold:builder

	klog.Info("Starting the Cmd.")

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		klog.Exitf("Manager exited with non-zero code: %v", err)
	}
}
