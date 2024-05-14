package infracluster

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	"k8s.io/client-go/rest"
	awsv1 "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-capi-operator/e2e/framework"
	"github.com/openshift/cluster-capi-operator/pkg/controllers"
	"github.com/openshift/cluster-capi-operator/pkg/operatorstatus"
)

const (
	// Controller conditions for the Cluster Operator resource
	InfraClusterControllerAvailableCondition = "InfraClusterControllerAvailable"
	InfraClusterControllerDegradedCondition  = "InfraClusterControllerDegraded"

	defaultCAPINamespace              = "openshift-cluster-api"
	providerConfigMapLabelVersionKey  = "provider.cluster.x-k8s.io/version"
	providerConfigMapLabelTypeKey     = "provider.cluster.x-k8s.io/type"
	providerConfigMapLabelNameKey     = "provider.cluster.x-k8s.io/name"
	ownedProviderComponentName        = "cluster.x-k8s.io/provider"
	imagePlaceholder                  = "to.be/replaced:v99"
	openshiftInfrastructureObjectName = "cluster"
	defaultInfraClusterName           = "cluster"
	notNamespaced                     = ""
	clusterOperatorName               = "cluster-api"
	defaultCoreProviderComponentName  = "cluster-api"
)

type InfraClusterController struct {
	operatorstatus.ClusterOperatorStatusClient
	Scheme   *runtime.Scheme
	Images   map[string]string
	RestCfg  *rest.Config
	Platform configv1.PlatformType
	Infra    configv1.Infrastructure
}

// Reconcile reconciles the cluster-api ClusterOperator object.
func (r *InfraClusterController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithName("InfraClusterController")

	res, err := r.reconcile(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error during reconcile: %w", err)
	}

	if err := r.setAvailableCondition(ctx, log); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set conditions for InfraCluster Controller: %w", err)
	}

	return res, nil
}

func (r *InfraClusterController) createAWSCluster(ctx context.Context) error {

	apiUrl, err := url.Parse(r.Infra.Status.APIServerInternalURL)
	if err != nil {
		return fmt.Errorf("failed to parse apiUrl: %w", err)
	}

	port, err := strconv.ParseInt(apiUrl.Port(), 10, 32)
	if err != nil {
		return fmt.Errorf("failed to parse apiUrl port: %w", err)
	}

	if r.Infra.Status.PlatformStatus == nil {
		return fmt.Errorf("infrastructure PlatformStatus should not be nil: %w", err)
	}

	awsCluster := &awsv1.AWSCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      defaultInfraClusterName,
			Namespace: framework.CAPINamespace,
		},
		Spec: awsv1.AWSClusterSpec{
			Region: r.Infra.Status.PlatformStatus.AWS.Region,
			ControlPlaneEndpoint: clusterv1.APIEndpoint{
				Host: apiUrl.Hostname(),
				Port: int32(port),
			},
		},
	}

	if err := r.Create(ctx, awsCluster); err != nil {
		return fmt.Errorf("failed to create %q: %w", awsCluster.Kind, err)
	}

	return nil
}

// reconcile performs the main business logic for installing Cluster API components in the cluster.
// Notably it fetches CAPI providers "transport" ConfigMap(s) matching the required labels,
// it extracts from those ConfigMaps the embedded CAPI providers manifests for the components
// and it applies them to the cluster.
//
//nolint:unparam
func (r *InfraClusterController) reconcile(ctx context.Context) (ctrl.Result, error) {
	switch r.Platform {
	case configv1.AWSPlatformType:
		r.reconcileAWSCluster(ctx)
	case configv1.GCPPlatformType:
	case configv1.PowerVSPlatformType:
	case configv1.VSpherePlatformType:
	case configv1.OpenStackPlatformType:
	default:
		klog.Infof("detected platform %q is not supported, skipping capi controllers setup", r.Platform)
	}
	return ctrl.Result{}, nil
}

func (r *InfraClusterController) reconcileAWSCluster(ctx context.Context) (ctrl.Result, error) {
	target := awsv1.AWSCluster{ObjectMeta: metav1.ObjectMeta{
		Name:      defaultInfraClusterName,
		Namespace: defaultCAPINamespace,
	}}
	err := r.Get(ctx, client.ObjectKeyFromObject(&target), &target)
	if err != nil && errors.IsNotFound(err) {
		if err := r.createAWSCluster(ctx); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to generate %q: %w", target.Kind, err)
		}
	} else if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get %q: %w", target.Kind, err)
	}
	return ctrl.Result{}, nil
}

// setAvailableCondition sets the ClusterOperator status condition to Available.
func (r *InfraClusterController) setAvailableCondition(ctx context.Context, log logr.Logger) error {
	co, err := r.GetOrCreateClusterOperator(ctx)
	if err != nil {
		return err
	}

	conds := []configv1.ClusterOperatorStatusCondition{
		operatorstatus.NewClusterOperatorStatusCondition(InfraClusterControllerAvailableCondition, configv1.ConditionTrue, operatorstatus.ReasonAsExpected,
			"InfraCluster Controller works as expected"),
		operatorstatus.NewClusterOperatorStatusCondition(InfraClusterControllerDegradedCondition, configv1.ConditionFalse, operatorstatus.ReasonAsExpected,
			"InfraCluster Controller works as expected"),
	}

	co.Status.Versions = []configv1.OperandVersion{{Name: controllers.OperatorVersionKey, Version: r.ReleaseVersion}}
	log.V(2).Info("InfraCluster Controller is Available")
	return r.SyncStatus(ctx, co, conds)
}

// setAvailableCondition sets the ClusterOperator status condition to Degraded.
func (r *InfraClusterController) setDegradedCondition(ctx context.Context, log logr.Logger) error {
	co, err := r.GetOrCreateClusterOperator(ctx)
	if err != nil {
		return err
	}

	conds := []configv1.ClusterOperatorStatusCondition{
		operatorstatus.NewClusterOperatorStatusCondition(InfraClusterControllerAvailableCondition, configv1.ConditionFalse, operatorstatus.ReasonSyncFailed,
			"InfraCluster Controller failed install"),
		operatorstatus.NewClusterOperatorStatusCondition(InfraClusterControllerDegradedCondition, configv1.ConditionTrue, operatorstatus.ReasonSyncFailed,
			"InfraCluster Controller failed install"),
	}

	co.Status.Versions = []configv1.OperandVersion{{Name: controllers.OperatorVersionKey, Version: r.ReleaseVersion}}
	log.Info("InfraCluster Controller is Degraded")
	return r.SyncStatus(ctx, co, conds)
}

// SetupWithManager sets up the controller with the Manager.
func (r *InfraClusterController) SetupWithManager(mgr ctrl.Manager) error {
	build := ctrl.NewControllerManagedBy(mgr).
		For(&configv1.ClusterOperator{}, builder.WithPredicates(clusterOperatorPredicates()))
		// TODO: write a function that based the Infra returns a watch to the cloud specific InfraClusters objects.

	return build.Complete(r)
}
