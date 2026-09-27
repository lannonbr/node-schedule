package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/robfig/cron/v3"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	automationv1alpha1 "github.com/lannonbr/node-schedule/api/v1alpha1"
)

const (
	labelNodeSchedule = "automation.lannonbr.com/node-schedule"
	scriptRefIndex    = "spec.scriptConfigMapRef.name"
	secretRefIndex    = "spec.secretRefs"
	readyCondition    = "Ready"
	defaultTimeZone   = "Etc/UTC"
)

var (
	defaultActiveDeadlineSeconds   int64 = 600
	defaultStartingDeadlineSeconds int64 = 300
	defaultBackoffLimit            int32 = 2
	defaultSuccessHistory          int32 = 3
	defaultFailureHistory          int32 = 3
	readOnlyMode                   int32 = 0444
	runAsUser                      int64 = 1000
)

// NodeScheduleReconciler turns NodeSchedules into CronJobs.
type NodeScheduleReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	NodeImage string
}

// +kubebuilder:rbac:groups=automation.lannonbr.com,resources=nodeschedules,verbs=get;list;watch
// +kubebuilder:rbac:groups=automation.lannonbr.com,resources=nodeschedules/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=automation.lannonbr.com,resources=nodeschedules/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps;secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch;update
// +kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch

func (r *NodeScheduleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	var schedule automationv1alpha1.NodeSchedule
	if err := r.Get(ctx, req.NamespacedName, &schedule); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	validationErr := r.validate(ctx, &schedule)
	if validationErr != nil {
		if validationErr.retry {
			return ctrl.Result{}, validationErr
		}
		if err := r.reconcileInvalid(ctx, &schedule, validationErr); err != nil {
			return ctrl.Result{}, err
		}
		r.Recorder.Event(&schedule, corev1.EventTypeWarning, validationErr.reason, validationErr.Error())
		return ctrl.Result{}, nil
	}

	changed, err := r.reconcileCronJob(ctx, &schedule, false)
	if err != nil {
		_ = r.setReady(ctx, &schedule, metav1.ConditionFalse, "ReconcileFailed", err.Error())
		return ctrl.Result{}, err
	}
	if changed {
		r.Recorder.Event(&schedule, corev1.EventTypeNormal, "CronJobReconciled", "CronJob was created or updated")
	}

	if err := r.refreshRunStatus(ctx, &schedule); err != nil {
		return ctrl.Result{}, err
	}
	message := "CronJob is ready"
	if schedule.Spec.Suspend {
		message = "CronJob is suspended by spec"
	}
	if err := r.setReady(ctx, &schedule, metav1.ConditionTrue, "DependenciesReady", message); err != nil {
		return ctrl.Result{}, err
	}
	log.V(1).Info("reconciled NodeSchedule", "cronJob", schedule.Name)
	return ctrl.Result{}, nil
}

type configError struct {
	reason       string
	message      string
	canReconcile bool
	retry        bool
}

func (e *configError) Error() string { return e.message }

func (r *NodeScheduleReconciler) validate(ctx context.Context, schedule *automationv1alpha1.NodeSchedule) *configError {
	if _, err := cron.ParseStandard(schedule.Spec.Schedule); err != nil {
		return &configError{reason: "InvalidSchedule", message: fmt.Sprintf("invalid schedule: %v", err)}
	}
	timeZone := schedule.Spec.TimeZone
	if timeZone == "" {
		timeZone = defaultTimeZone
	}
	if _, err := time.LoadLocation(timeZone); err != nil {
		return &configError{reason: "InvalidTimeZone", message: fmt.Sprintf("invalid timeZone %q: %v", timeZone, err)}
	}
	if schedule.Spec.ScriptConfigMapRef.Name == "" {
		return &configError{reason: "InvalidScriptReference", message: "spec.scriptConfigMapRef.name is required"}
	}

	seen := make(map[string]struct{}, len(schedule.Spec.Env))
	for _, env := range schedule.Spec.Env {
		if env.Name == "" {
			return &configError{reason: "InvalidEnvironment", message: "environment variable name must not be empty"}
		}
		if _, found := seen[env.Name]; found {
			return &configError{reason: "InvalidEnvironment", message: fmt.Sprintf("environment variable %q is duplicated", env.Name)}
		}
		seen[env.Name] = struct{}{}
		if env.ValueFrom == nil {
			continue
		}
		if env.Value != "" || env.ValueFrom.SecretKeyRef == nil || env.ValueFrom.ConfigMapKeyRef != nil ||
			env.ValueFrom.FieldRef != nil || env.ValueFrom.ResourceFieldRef != nil {
			return &configError{reason: "InvalidEnvironment", message: fmt.Sprintf("environment variable %q may only use value or valueFrom.secretKeyRef", env.Name)}
		}
		if env.ValueFrom.SecretKeyRef.Name == "" || env.ValueFrom.SecretKeyRef.Key == "" {
			return &configError{reason: "InvalidEnvironment", message: fmt.Sprintf("environment variable %q has an incomplete secretKeyRef", env.Name)}
		}
	}
	for name, request := range schedule.Spec.Resources.Requests {
		if limit, found := schedule.Spec.Resources.Limits[name]; found && request.Cmp(limit) > 0 {
			return &configError{reason: "InvalidResources", message: fmt.Sprintf("resource %q request exceeds its limit", name)}
		}
	}

	var source corev1.ConfigMap
	key := types.NamespacedName{Namespace: schedule.Namespace, Name: schedule.Spec.ScriptConfigMapRef.Name}
	if err := r.Get(ctx, key, &source); err != nil {
		if apierrors.IsNotFound(err) {
			return &configError{reason: "ConfigMapNotFound", message: fmt.Sprintf("script ConfigMap %q was not found", key.Name), canReconcile: true}
		}
		return &configError{reason: "DependencyCheckFailed", message: err.Error(), retry: true}
	}
	if strings.TrimSpace(source.Data["script.js"]) == "" {
		return &configError{reason: "ScriptNotFound", message: fmt.Sprintf("ConfigMap %q must contain a non-empty script.js key", key.Name), canReconcile: true}
	}

	for _, env := range schedule.Spec.Env {
		if env.ValueFrom == nil {
			continue
		}
		ref := env.ValueFrom.SecretKeyRef
		var secret corev1.Secret
		secretKey := types.NamespacedName{Namespace: schedule.Namespace, Name: ref.Name}
		if err := r.Get(ctx, secretKey, &secret); err != nil {
			if apierrors.IsNotFound(err) {
				return &configError{reason: "SecretNotFound", message: fmt.Sprintf("Secret %q for environment variable %q was not found", ref.Name, env.Name), canReconcile: true}
			}
			return &configError{reason: "DependencyCheckFailed", message: err.Error(), retry: true}
		}
		if _, found := secret.Data[ref.Key]; !found {
			return &configError{reason: "SecretKeyNotFound", message: fmt.Sprintf("Secret %q does not contain key %q", ref.Name, ref.Key), canReconcile: true}
		}
	}
	return nil
}

func (r *NodeScheduleReconciler) reconcileInvalid(ctx context.Context, schedule *automationv1alpha1.NodeSchedule, validationErr *configError) error {
	if validationErr.canReconcile {
		if _, err := r.reconcileCronJob(ctx, schedule, true); err != nil {
			return err
		}
	} else {
		if err := r.suspendExistingCronJob(ctx, schedule); err != nil {
			return err
		}
	}
	if err := r.refreshRunStatus(ctx, schedule); err != nil {
		return err
	}
	return r.setReady(ctx, schedule, metav1.ConditionFalse, validationErr.reason, validationErr.message)
}

func (r *NodeScheduleReconciler) reconcileCronJob(ctx context.Context, schedule *automationv1alpha1.NodeSchedule, forceSuspend bool) (bool, error) {
	desired := r.desiredCronJob(schedule, forceSuspend)
	key := client.ObjectKeyFromObject(desired)
	var existing batchv1.CronJob
	if err := r.Get(ctx, key, &existing); err != nil {
		if !apierrors.IsNotFound(err) {
			return false, err
		}
		if err := controllerutil.SetControllerReference(schedule, desired, r.Scheme); err != nil {
			return false, err
		}
		return true, r.Create(ctx, desired)
	}
	if !metav1.IsControlledBy(&existing, schedule) {
		return false, fmt.Errorf("CronJob %s/%s already exists and is not owned by this NodeSchedule", existing.Namespace, existing.Name)
	}
	changed := !equality.Semantic.DeepEqual(existing.Spec, desired.Spec) || !equality.Semantic.DeepEqual(existing.Labels, desired.Labels)
	if !changed {
		return false, nil
	}
	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	return true, r.Update(ctx, &existing)
}

func (r *NodeScheduleReconciler) suspendExistingCronJob(ctx context.Context, schedule *automationv1alpha1.NodeSchedule) error {
	var cronJob batchv1.CronJob
	key := types.NamespacedName{Namespace: schedule.Namespace, Name: schedule.Name}
	if err := r.Get(ctx, key, &cronJob); err != nil {
		return client.IgnoreNotFound(err)
	}
	if !metav1.IsControlledBy(&cronJob, schedule) || (cronJob.Spec.Suspend != nil && *cronJob.Spec.Suspend) {
		return nil
	}
	cronJob.Spec.Suspend = pointer(true)
	return r.Update(ctx, &cronJob)
}

func (r *NodeScheduleReconciler) desiredCronJob(schedule *automationv1alpha1.NodeSchedule, forceSuspend bool) *batchv1.CronJob {
	timeZone := schedule.Spec.TimeZone
	if timeZone == "" {
		timeZone = defaultTimeZone
	}
	execution := schedule.Spec.Execution
	activeDeadline := valueOr(execution.ActiveDeadlineSeconds, defaultActiveDeadlineSeconds)
	startingDeadline := valueOr(execution.StartingDeadlineSeconds, defaultStartingDeadlineSeconds)
	backoffLimit := valueOr(execution.BackoffLimit, defaultBackoffLimit)
	successHistory := valueOr(execution.SuccessfulJobsHistoryLimit, defaultSuccessHistory)
	failureHistory := valueOr(execution.FailedJobsHistoryLimit, defaultFailureHistory)
	resources := defaultResources(schedule.Spec.Resources)
	falseValue := false
	trueValue := true
	suspend := schedule.Spec.Suspend || forceSuspend
	jobLabels := map[string]string{labelNodeSchedule: schedule.Name}
	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: schedule.Name, Namespace: schedule.Namespace, Labels: jobLabels},
		Spec: batchv1.CronJobSpec{
			Schedule:                   schedule.Spec.Schedule,
			TimeZone:                   &timeZone,
			ConcurrencyPolicy:          batchv1.ForbidConcurrent,
			Suspend:                    &suspend,
			StartingDeadlineSeconds:    &startingDeadline,
			SuccessfulJobsHistoryLimit: &successHistory,
			FailedJobsHistoryLimit:     &failureHistory,
			JobTemplate: batchv1.JobTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: jobLabels},
				Spec: batchv1.JobSpec{
					BackoffLimit:          &backoffLimit,
					ActiveDeadlineSeconds: &activeDeadline,
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: jobLabels},
						Spec: corev1.PodSpec{
							AutomountServiceAccountToken: &falseValue,
							RestartPolicy:                corev1.RestartPolicyNever,
							SecurityContext:              &corev1.PodSecurityContext{RunAsNonRoot: &trueValue, RunAsUser: &runAsUser, RunAsGroup: &runAsUser, SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}},
							Containers: []corev1.Container{{
								Name:            "script",
								Image:           r.NodeImage,
								ImagePullPolicy: corev1.PullIfNotPresent,
								Command:         []string{"node", "/scripts/script.js"},
								Env:             deepCopyEnv(schedule.Spec.Env),
								Resources:       *resources,
								VolumeMounts:    []corev1.VolumeMount{{Name: "script", MountPath: "/scripts", ReadOnly: true}},
								SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: &falseValue, ReadOnlyRootFilesystem: &trueValue, Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}},
							}},
							Volumes: []corev1.Volume{{Name: "script", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: schedule.Spec.ScriptConfigMapRef, DefaultMode: &readOnlyMode, Items: []corev1.KeyToPath{{Key: "script.js", Path: "script.js"}}}}}},
						},
					},
				},
			},
		},
	}
}

func defaultResources(configured corev1.ResourceRequirements) *corev1.ResourceRequirements {
	resources := configured.DeepCopy()
	if resources.Requests == nil {
		resources.Requests = corev1.ResourceList{}
	}
	if resources.Limits == nil {
		resources.Limits = corev1.ResourceList{}
	}
	defaults := []struct {
		name    corev1.ResourceName
		request string
		limit   string
	}{
		{corev1.ResourceCPU, "25m", "250m"},
		{corev1.ResourceMemory, "64Mi", "128Mi"},
	}
	for _, value := range defaults {
		request, hasRequest := resources.Requests[value.name]
		limit, hasLimit := resources.Limits[value.name]
		defaultRequest := resource.MustParse(value.request)
		defaultLimit := resource.MustParse(value.limit)
		if !hasRequest {
			if hasLimit && limit.Cmp(defaultRequest) < 0 {
				resources.Requests[value.name] = limit.DeepCopy()
			} else {
				resources.Requests[value.name] = defaultRequest
			}
		}
		if !hasLimit {
			if hasRequest && request.Cmp(defaultLimit) > 0 {
				resources.Limits[value.name] = request.DeepCopy()
			} else {
				resources.Limits[value.name] = defaultLimit
			}
		}
	}
	return resources
}

func (r *NodeScheduleReconciler) refreshRunStatus(ctx context.Context, schedule *automationv1alpha1.NodeSchedule) error {
	before := schedule.DeepCopy().Status
	var jobs batchv1.JobList
	selector := labels.SelectorFromSet(map[string]string{labelNodeSchedule: schedule.Name})
	if err := r.List(ctx, &jobs, client.InNamespace(schedule.Namespace), client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return err
	}
	schedule.Status.CronJobName = schedule.Name
	if len(jobs.Items) == 0 {
		if equality.Semantic.DeepEqual(before, schedule.Status) {
			return nil
		}
		return r.Status().Update(ctx, schedule)
	}
	sort.Slice(jobs.Items, func(i, j int) bool { return jobTime(&jobs.Items[i]).After(jobTime(&jobs.Items[j])) })
	mostRecent := &jobs.Items[0]
	schedule.Status.MostRecentJobName = mostRecent.Name
	schedule.Status.LastRunPhase, schedule.Status.Reason = jobPhase(mostRecent)
	for i := range jobs.Items {
		job := &jobs.Items[i]
		if job.Status.StartTime != nil && later(job.Status.StartTime, schedule.Status.LastStartTime) {
			schedule.Status.LastStartTime = job.Status.StartTime.DeepCopy()
		}
		finishedAt := jobFinishedTime(job)
		if finishedAt != nil && later(finishedAt, schedule.Status.LastCompletionTime) {
			schedule.Status.LastCompletionTime = finishedAt.DeepCopy()
		}
		phase, _ := jobPhase(job)
		if phase == "Succeeded" && finishedAt != nil && later(finishedAt, schedule.Status.LastSuccessTime) {
			schedule.Status.LastSuccessTime = finishedAt.DeepCopy()
		}
		if phase == "Failed" && finishedAt != nil && later(finishedAt, schedule.Status.LastFailureTime) {
			schedule.Status.LastFailureTime = finishedAt.DeepCopy()
		}
	}
	if equality.Semantic.DeepEqual(before, schedule.Status) {
		return nil
	}
	return r.Status().Update(ctx, schedule)
}

func (r *NodeScheduleReconciler) setReady(ctx context.Context, schedule *automationv1alpha1.NodeSchedule, status metav1.ConditionStatus, reason, message string) error {
	before := schedule.DeepCopy().Status
	schedule.Status.ObservedGeneration = schedule.Generation
	meta.SetStatusCondition(&schedule.Status.Conditions, metav1.Condition{Type: readyCondition, Status: status, Reason: reason, Message: message, ObservedGeneration: schedule.Generation})
	if equality.Semantic.DeepEqual(before, schedule.Status) {
		return nil
	}
	return r.Status().Update(ctx, schedule)
}

func jobTime(job *batchv1.Job) time.Time {
	if job.Status.StartTime != nil {
		return job.Status.StartTime.Time
	}
	return job.CreationTimestamp.Time
}

func jobPhase(job *batchv1.Job) (string, string) {
	for _, condition := range job.Status.Conditions {
		if condition.Status != corev1.ConditionTrue {
			continue
		}
		if condition.Type == batchv1.JobComplete {
			return "Succeeded", condition.Reason
		}
		if condition.Type == batchv1.JobFailed {
			return "Failed", condition.Reason
		}
	}
	if job.Status.Succeeded > 0 {
		return "Succeeded", "Completed"
	}
	if job.Status.Active > 0 {
		return "Running", "Active"
	}
	return "Pending", "Pending"
}

func jobFinishedTime(job *batchv1.Job) *metav1.Time {
	if job.Status.CompletionTime != nil {
		return job.Status.CompletionTime
	}
	var finishedAt *metav1.Time
	for i := range job.Status.Conditions {
		condition := &job.Status.Conditions[i]
		if condition.Status != corev1.ConditionTrue || (condition.Type != batchv1.JobComplete && condition.Type != batchv1.JobFailed) {
			continue
		}
		if finishedAt == nil || condition.LastTransitionTime.After(finishedAt.Time) {
			finishedAt = &condition.LastTransitionTime
		}
	}
	return finishedAt
}

func later(candidate, current *metav1.Time) bool {
	return current == nil || candidate.After(current.Time)
}

func pointer[T any](value T) *T { return &value }

func valueOr[T any](value *T, fallback T) T {
	if value == nil {
		return fallback
	}
	return *value
}

func deepCopyEnv(env []corev1.EnvVar) []corev1.EnvVar {
	result := make([]corev1.EnvVar, len(env))
	for i := range env {
		env[i].DeepCopyInto(&result[i])
	}
	return result
}

func (r *NodeScheduleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()
	if err := mgr.GetFieldIndexer().IndexField(ctx, &automationv1alpha1.NodeSchedule{}, scriptRefIndex, func(object client.Object) []string {
		return []string{object.(*automationv1alpha1.NodeSchedule).Spec.ScriptConfigMapRef.Name}
	}); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(ctx, &automationv1alpha1.NodeSchedule{}, secretRefIndex, func(object client.Object) []string {
		schedule := object.(*automationv1alpha1.NodeSchedule)
		var names []string
		seen := map[string]struct{}{}
		for _, env := range schedule.Spec.Env {
			if env.ValueFrom == nil || env.ValueFrom.SecretKeyRef == nil {
				continue
			}
			name := env.ValueFrom.SecretKeyRef.Name
			if _, found := seen[name]; !found {
				names = append(names, name)
				seen[name] = struct{}{}
			}
		}
		return names
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&automationv1alpha1.NodeSchedule{}).
		Owns(&batchv1.CronJob{}).
		Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(r.requestsForField(scriptRefIndex))).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.requestsForField(secretRefIndex))).
		Watches(&batchv1.Job{}, handler.EnqueueRequestsFromMapFunc(requestForLabeledJob)).
		Complete(r)
}

func (r *NodeScheduleReconciler) requestsForField(field string) handler.MapFunc {
	return func(ctx context.Context, object client.Object) []reconcile.Request {
		var schedules automationv1alpha1.NodeScheduleList
		if err := r.List(ctx, &schedules, client.InNamespace(object.GetNamespace()), client.MatchingFields{field: object.GetName()}); err != nil {
			return nil
		}
		requests := make([]reconcile.Request, 0, len(schedules.Items))
		for i := range schedules.Items {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&schedules.Items[i])})
		}
		return requests
	}
}

func requestForLabeledJob(_ context.Context, object client.Object) []reconcile.Request {
	name := object.GetLabels()[labelNodeSchedule]
	if name == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: types.NamespacedName{Namespace: object.GetNamespace(), Name: name}}}
}
