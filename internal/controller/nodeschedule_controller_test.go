package controller

import (
	"context"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	automationv1alpha1 "github.com/lannonbr/node-schedule/api/v1alpha1"
)

func TestDesiredCronJobAppliesSafeDefaults(t *testing.T) {
	reconciler := &NodeScheduleReconciler{NodeImage: "node@example-digest"}
	schedule := &automationv1alpha1.NodeSchedule{
		ObjectMeta: metav1.ObjectMeta{Name: "digest", Namespace: "automation"},
		Spec: automationv1alpha1.NodeScheduleSpec{
			Schedule:           "0 7 * * *",
			ScriptConfigMapRef: corev1.LocalObjectReference{Name: "digest-script"},
		},
	}

	cronJob := reconciler.desiredCronJob(schedule, false)
	if cronJob.Spec.ConcurrencyPolicy != "Forbid" {
		t.Fatalf("expected Forbid concurrency policy, got %q", cronJob.Spec.ConcurrencyPolicy)
	}
	if cronJob.Spec.TimeZone == nil || *cronJob.Spec.TimeZone != "Etc/UTC" {
		t.Fatalf("expected UTC default, got %v", cronJob.Spec.TimeZone)
	}
	if cronJob.Spec.StartingDeadlineSeconds == nil || *cronJob.Spec.StartingDeadlineSeconds != 300 {
		t.Fatalf("expected 300 second starting deadline")
	}
	job := cronJob.Spec.JobTemplate.Spec
	if job.ActiveDeadlineSeconds == nil || *job.ActiveDeadlineSeconds != 600 {
		t.Fatalf("expected 600 second active deadline")
	}
	if job.BackoffLimit == nil || *job.BackoffLimit != 2 {
		t.Fatalf("expected backoff limit 2")
	}
	pod := job.Template.Spec
	if pod.AutomountServiceAccountToken == nil || *pod.AutomountServiceAccountToken {
		t.Fatalf("service account token must not be mounted")
	}
	if got := pod.Containers[0].Command; len(got) != 2 || got[0] != "node" || got[1] != "/scripts/script.js" {
		t.Fatalf("unexpected command: %v", got)
	}
	if got := pod.Volumes[0].ConfigMap.Name; got != "digest-script" {
		t.Fatalf("unexpected source ConfigMap %q", got)
	}
}

func TestDesiredCronJobCanBeForceSuspended(t *testing.T) {
	reconciler := &NodeScheduleReconciler{NodeImage: "node@example-digest"}
	schedule := &automationv1alpha1.NodeSchedule{
		ObjectMeta: metav1.ObjectMeta{Name: "digest", Namespace: "automation"},
		Spec:       automationv1alpha1.NodeScheduleSpec{Schedule: "@daily", ScriptConfigMapRef: corev1.LocalObjectReference{Name: "script"}},
	}
	cronJob := reconciler.desiredCronJob(schedule, true)
	if cronJob.Spec.Suspend == nil || !*cronJob.Spec.Suspend {
		t.Fatal("expected force-suspended CronJob")
	}
}

func TestReconcileSuspendsUntilScriptConfigMapExists(t *testing.T) {
	testScheme := runtime.NewScheme()
	if err := corev1.AddToScheme(testScheme); err != nil {
		t.Fatal(err)
	}
	if err := batchv1.AddToScheme(testScheme); err != nil {
		t.Fatal(err)
	}
	if err := automationv1alpha1.AddToScheme(testScheme); err != nil {
		t.Fatal(err)
	}

	schedule := &automationv1alpha1.NodeSchedule{
		ObjectMeta: metav1.ObjectMeta{Name: "digest", Namespace: "automation", UID: types.UID("test-uid"), Generation: 1},
		Spec: automationv1alpha1.NodeScheduleSpec{
			Schedule:           "0 7 * * *",
			ScriptConfigMapRef: corev1.LocalObjectReference{Name: "digest-script"},
		},
	}
	testClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithStatusSubresource(&automationv1alpha1.NodeSchedule{}).
		WithObjects(schedule).
		Build()
	reconciler := &NodeScheduleReconciler{
		Client: testClient, Scheme: testScheme, Recorder: record.NewFakeRecorder(10), NodeImage: "node@example-digest",
	}
	request := ctrl.Request{NamespacedName: types.NamespacedName{Name: schedule.Name, Namespace: schedule.Namespace}}
	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatalf("reconcile with missing ConfigMap: %v", err)
	}

	var cronJob batchv1.CronJob
	if err := testClient.Get(context.Background(), request.NamespacedName, &cronJob); err != nil {
		t.Fatalf("get generated CronJob: %v", err)
	}
	if cronJob.Spec.Suspend == nil || !*cronJob.Spec.Suspend {
		t.Fatal("CronJob should be suspended while its ConfigMap is missing")
	}
	var current automationv1alpha1.NodeSchedule
	if err := testClient.Get(context.Background(), request.NamespacedName, &current); err != nil {
		t.Fatal(err)
	}
	condition := meta.FindStatusCondition(current.Status.Conditions, readyCondition)
	if condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != "ConfigMapNotFound" {
		t.Fatalf("unexpected Ready condition: %#v", condition)
	}

	source := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "digest-script", Namespace: "automation"},
		Data:       map[string]string{"script.js": "console.log('ok')"},
	}
	if err := testClient.Create(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatalf("reconcile after creating ConfigMap: %v", err)
	}
	if err := testClient.Get(context.Background(), request.NamespacedName, &cronJob); err != nil {
		t.Fatal(err)
	}
	if cronJob.Spec.Suspend == nil || *cronJob.Spec.Suspend {
		t.Fatal("CronJob should resume after its dependency is repaired")
	}
	if err := testClient.Get(context.Background(), request.NamespacedName, &current); err != nil {
		t.Fatal(err)
	}
	condition = meta.FindStatusCondition(current.Status.Conditions, readyCondition)
	if condition == nil || condition.Status != metav1.ConditionTrue {
		t.Fatalf("unexpected repaired Ready condition: %#v", condition)
	}
}
