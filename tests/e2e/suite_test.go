//go:build e2e
// +build e2e

/*
Copyright 2026.

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

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	costv1alpha1 "github.com/youssef-ar/namespace-cost-governor/api/v1alpha1"
	"github.com/youssef-ar/namespace-cost-governor/internal/actions"
	"github.com/youssef-ar/namespace-cost-governor/internal/controller"
	"github.com/youssef-ar/namespace-cost-governor/internal/metrics"
	"github.com/youssef-ar/namespace-cost-governor/internal/report"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	e2eCtx    context.Context
	e2eCancel context.CancelFunc
	e2eEnv    *envtest.Environment
	e2eClient client.Client
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "NamespaceBudget envtest suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	e2eCtx, e2eCancel = context.WithCancel(context.Background())

	s := runtime.NewScheme()
	Expect(costv1alpha1.AddToScheme(s)).To(Succeed())
	Expect(appsv1.AddToScheme(s)).To(Succeed())
	Expect(corev1.AddToScheme(s)).To(Succeed())

	e2eEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	if assets := os.Getenv("KUBEBUILDER_ASSETS"); assets != "" {
		e2eEnv.BinaryAssetsDirectory = assets
	} else if assets := firstEnvtestBinaryDir(); assets != "" {
		e2eEnv.BinaryAssetsDirectory = assets
	}

	cfg, err := e2eEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	e2eClient, err = client.New(cfg, client.Options{Scheme: s})
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	e2eCancel()
	Expect(e2eEnv.Stop()).To(Succeed())
})

var _ = Describe("NamespaceBudget reconciliation", Ordered, func() {
	var (
		namespace  string
		prom       *httptest.Server
		reconciler *controller.NamespaceBudgetReconciler
	)

	BeforeEach(func() {
		namespace = fmt.Sprintf("cost-e2e-%d", time.Now().UnixNano())
		currentNamespace = namespace
		Expect(e2eClient.Create(e2eCtx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})).To(Succeed())

		prom = newPrometheusServer()
		reconciler = &controller.NamespaceBudgetReconciler{
			Client:           e2eClient,
			Scheme:           e2eClient.Scheme(),
			PrometheusClient: metrics.NewClient(prom.URL),
		}
	})

	AfterEach(func() {
		prom.Close()
		Expect(e2eClient.Delete(e2eCtx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})).To(Succeed())
	})

	It("sets Exceeded when Prometheus reports high usage", func() {
		budget := createExceededBudget(namespace, "tight-budget", nil)
		_, err := reconcileBudget(reconciler, budget)
		Expect(err).NotTo(HaveOccurred())

		Expect(getBudget(budget.Name).Status.Phase).To(Equal("Exceeded"))
	})

	It("scales an idle Deployment to zero and records ownership annotations", func() {
		createDeployment(namespace, "idle-deployment", 3, nil)
		budget := createExceededBudget(namespace, "idle-budget", nil)

		_, err := reconcileBudget(reconciler, budget)
		Expect(err).NotTo(HaveOccurred())

		deployment := getDeployment("idle-deployment")
		Expect(*deployment.Spec.Replicas).To(Equal(int32(0)))
		Expect(deployment.Annotations[actions.AnnotationScaledDownBy]).To(Equal("namespace-cost-governor"))
		Expect(deployment.Annotations[actions.AnnotationScaledDownAt]).NotTo(BeEmpty())
		Expect(deployment.Annotations[actions.AnnotationOriginalReplicas]).To(Equal("3"))
	})

	It("does not act twice when the scale-down marker is removed", func() {
		createDeployment(namespace, "idle-deployment", 3, nil)
		budget := createExceededBudget(namespace, "idempotent-budget", nil)
		_, err := reconcileBudget(reconciler, budget)
		Expect(err).NotTo(HaveOccurred())

		deployment := getDeployment("idle-deployment")
		delete(deployment.Annotations, actions.AnnotationScaledDownBy)
		Expect(e2eClient.Update(e2eCtx, deployment)).To(Succeed())

		_, err = reconciler.Reconcile(e2eCtx, requestFor(budget))
		Expect(err).NotTo(HaveOccurred())
		deployment = getDeployment("idle-deployment")
		Expect(*deployment.Spec.Replicas).To(Equal(int32(0)))
		Expect(deployment.Annotations).NotTo(HaveKey(actions.AnnotationScaledDownBy))
	})

	It("leaves an excluded Deployment unchanged", func() {
		createDeployment(namespace, "excluded-deployment", 2, nil)
		budget := createExceededBudget(namespace, "excluded-budget", []costv1alpha1.Exclusion{{Name: "excluded-deployment"}})

		_, err := reconcileBudget(reconciler, budget)
		Expect(err).NotTo(HaveOccurred())

		deployment := getDeployment("excluded-deployment")
		Expect(*deployment.Spec.Replicas).To(Equal(int32(2)))
		Expect(deployment.Annotations).NotTo(HaveKey(actions.AnnotationScaledDownBy))
	})

	It("transitions OK, Warning, Exceeded, and Suspended with stable condition timestamps", func() {
		budget := createExceededBudget(namespace, "phase-budget", nil)
		_, err := reconciler.Reconcile(e2eCtx, requestFor(budget))
		Expect(err).NotTo(HaveOccurred())
		budget = getBudget(budget.Name)
		budget.Spec.Monthly.Cpu = "100"
		Expect(e2eClient.Update(e2eCtx, budget)).To(Succeed())

		phases := []struct {
			coreHours string
			phase     string
		}{
			{"79.5", "OK"},
			{"80", "Warning"},
			{"100", "Exceeded"},
			{"120", "Suspended"},
		}
		var previousWarning, previousExceeded metav1.Time
		for i, expected := range phases {
			if i > 0 {
				time.Sleep(1100 * time.Millisecond)
			}
			_, err = setAccumulatedAndReconcile(reconciler, budget, expected.coreHours)
			Expect(err).NotTo(HaveOccurred())
			stored := getBudget(budget.Name)
			Expect(stored.Status.Phase).To(Equal(expected.phase))

			warning := conditionFor(stored, "UsageWarning")
			exceeded := conditionFor(stored, "BudgetExceeded")
			Expect(warning.Reason).To(Equal("BudgetThresholdReached"))
			Expect(exceeded.Reason).To(Equal("MonthlyBudgetExceeded"))
			if i > 0 {
				if expected.phase == "Warning" {
					Expect(warning.LastTransitionTime.After(previousWarning.Time)).To(BeTrue())
				} else {
					Expect(warning.LastTransitionTime).To(Equal(previousWarning))
				}
				if expected.phase == "Exceeded" {
					Expect(exceeded.LastTransitionTime.After(previousExceeded.Time)).To(BeTrue())
				} else {
					Expect(exceeded.LastTransitionTime).To(Equal(previousExceeded))
				}
			}
			previousWarning = warning.LastTransitionTime
			previousExceeded = exceeded.LastTransitionTime

			_, err = reconciler.Reconcile(e2eCtx, requestFor(budget))
			Expect(err).NotTo(HaveOccurred())
			reconciled := getBudget(budget.Name)
			Expect(conditionFor(reconciled, "UsageWarning").LastTransitionTime).To(Equal(previousWarning))
			Expect(conditionFor(reconciled, "BudgetExceeded").LastTransitionTime).To(Equal(previousExceeded))
		}
	})

	It("generates a CostReport with ordered consumers and estimated cost", func() {
		reportProm := newReportPrometheusServer()
		DeferCleanup(reportProm.Close)
		budget := createExceededBudget(namespace, "report-budget", nil)
		stored := getBudget(budget.Name)
		stored.Status.Accumulated.CoreHours = "10"
		stored.Status.Accumulated.GiBHours = "2"
		Expect(e2eClient.Status().Update(e2eCtx, stored)).To(Succeed())

		generator := report.NewGenerator(metrics.Client{BaseURL: reportProm.URL, Client: reportProm.Client()}, e2eClient, 1, 1)
		Expect(generator.Generate(e2eCtx, *stored)).To(Succeed())

		reports := &costv1alpha1.CostReportList{}
		Expect(e2eClient.List(e2eCtx, reports, client.InNamespace(namespace))).To(Succeed())
		Expect(reports.Items).To(HaveLen(1))
		generated := reports.Items[0]
		Expect(generated.Status.TopConsumers).To(HaveLen(2))
		Expect(generated.Status.TopConsumers[0].Workload).To(Equal("payments-api"))
		Expect(generated.Status.TopConsumers[1].Workload).To(Equal("batch-api"))
		Expect(generated.Status.TotalCost.EstimatedUSD).To(Equal("$12.00"))
		Expect(generated.Status.TopConsumers[0].EstimatedUSD).NotTo(Equal("$0.00"))
	})

	It("restores child Deployments during finalizer cleanup", func() {
		createDeployment(namespace, "cleanup-deployment", 3, nil)
		budget := createExceededBudget(namespace, "cleanup-budget", nil)
		_, err := reconcileBudget(reconciler, budget)
		Expect(err).NotTo(HaveOccurred())
		Expect(getDeployment("cleanup-deployment").Annotations).To(HaveKey(actions.AnnotationScaledDownBy))

		Expect(e2eClient.Delete(e2eCtx, budget)).To(Succeed())
		_, err = reconciler.Reconcile(e2eCtx, requestFor(budget))
		Expect(err).NotTo(HaveOccurred())

		deployment := getDeployment("cleanup-deployment")
		Expect(*deployment.Spec.Replicas).To(Equal(int32(3)))
		Expect(deployment.Annotations).NotTo(HaveKey(actions.AnnotationScaledDownBy))
		Expect(deployment.Annotations).NotTo(HaveKey(actions.AnnotationScaledDownAt))
		Expect(deployment.Annotations).NotTo(HaveKey(actions.AnnotationOriginalReplicas))
	})
})

func createExceededBudget(namespace, name string, exclusions []costv1alpha1.Exclusion) *costv1alpha1.NamespaceBudget {
	budget := &costv1alpha1.NamespaceBudget{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: costv1alpha1.NamespaceBudgetSpec{
			Monthly:       costv1alpha1.Usage{Cpu: "1", Memory: "0"},
			IdleThreshold: costv1alpha1.IdleThreshold{CpuPercent: 1, Window: "30m"},
			Exclusions:    exclusions,
		},
	}
	Expect(e2eClient.Create(e2eCtx, budget)).To(Succeed())
	return budget
}

func reconcileBudget(reconciler *controller.NamespaceBudgetReconciler, budget *costv1alpha1.NamespaceBudget) (ctrl.Result, error) {
	result, err := reconciler.Reconcile(e2eCtx, requestFor(budget))
	if err != nil {
		return result, err
	}

	stored := getBudget(budget.Name)
	stored.Status.Accumulated.CoreHours = "0.99"
	stored.Status.LastReconcile = metav1.NewTime(time.Now().Add(-time.Minute))
	if err := e2eClient.Status().Update(e2eCtx, stored); err != nil {
		return result, err
	}
	return reconciler.Reconcile(e2eCtx, requestFor(budget))
}

func setAccumulatedAndReconcile(reconciler *controller.NamespaceBudgetReconciler, budget *costv1alpha1.NamespaceBudget, coreHours string) (ctrl.Result, error) {
	stored := getBudget(budget.Name)
	stored.Status.Accumulated.CoreHours = coreHours
	stored.Status.LastReconcile = metav1.NewTime(time.Now())
	if err := e2eClient.Status().Update(e2eCtx, stored); err != nil {
		return ctrl.Result{}, err
	}
	return reconciler.Reconcile(e2eCtx, requestFor(budget))
}

func conditionFor(budget *costv1alpha1.NamespaceBudget, conditionType string) metav1.Condition {
	for _, condition := range budget.Status.Conditions {
		if condition.Type == conditionType {
			return condition
		}
	}
	Fail("condition not found: " + conditionType)
	return metav1.Condition{}
}

func createDeployment(namespace, name string, replicas int32, labels map[string]string) {
	Expect(e2eClient.Create(e2eCtx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "busybox:latest"}}}},
		},
	})).To(Succeed())
}

func getBudget(name string) *costv1alpha1.NamespaceBudget {
	budget := &costv1alpha1.NamespaceBudget{}
	Expect(e2eClient.Get(e2eCtx, types.NamespacedName{Name: name, Namespace: namespaceForCurrentSpec()}, budget)).To(Succeed())
	return budget
}

func getDeployment(name string) *appsv1.Deployment {
	deployment := &appsv1.Deployment{}
	Expect(e2eClient.Get(e2eCtx, types.NamespacedName{Name: name, Namespace: namespaceForCurrentSpec()}, deployment)).To(Succeed())
	return deployment
}

func namespaceForCurrentSpec() string { return currentNamespace }

var currentNamespace string

func requestFor(budget *costv1alpha1.NamespaceBudget) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Name: budget.Name, Namespace: budget.Namespace}}
}

func newPrometheusServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resultType := "vector"
		result := []map[string]interface{}{
			{"metric": map[string]string{"pod": "idle-deployment-abc123-xyz"}, "value": []interface{}{float64(time.Now().Unix()), "1.0"}},
			{"metric": map[string]string{"pod": "excluded-deployment-abc123-xyz"}, "value": []interface{}{float64(time.Now().Unix()), "1.0"}},
			{"metric": map[string]string{"pod": "cleanup-deployment-abc123-xyz"}, "value": []interface{}{float64(time.Now().Unix()), "1.0"}},
		}
		if r.URL.Path == "/api/v1/query_range" {
			resultType = "matrix"
			for i := range result {
				result[i] = map[string]interface{}{
					"metric": result[i]["metric"],
					"values": [][]interface{}{{float64(time.Now().Unix()), "0.0"}},
				}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "data": map[string]interface{}{"resultType": resultType, "result": result}})
	}))
}

func newReportPrometheusServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		memory := strings.Contains(r.URL.Query().Get("query"), "container_memory")
		first, second := "2", "1"
		if memory {
			first, second = "1073741824", "536870912"
		}
		result := []map[string]interface{}{
			{"metric": map[string]string{"pod": "payments-api-abc123-xyz"}, "values": [][]interface{}{{float64(time.Now().Unix()), first}}},
			{"metric": map[string]string{"pod": "batch-api-abc123-xyz"}, "values": [][]interface{}{{float64(time.Now().Unix()), second}}},
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "success", "data": map[string]interface{}{"resultType": "matrix", "result": result}})
	}))
}

func firstEnvtestBinaryDir() string {
	entries, err := os.ReadDir(filepath.Join("..", "..", "bin", "k8s"))
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join("..", "..", "bin", "k8s", entry.Name())
		}
	}
	return ""
}
