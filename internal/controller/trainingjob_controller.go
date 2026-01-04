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

package controller

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	batchv1 "github.com/Rory109/titan-scheduler/api/v1"
)

// TrainingJobReconciler reconciles a TrainingJob object
type TrainingJobReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=batch.rory109.com,resources=trainingjobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch.rory109.com,resources=trainingjobs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=batch.rory109.com,resources=trainingjobs/finalizers,verbs=update
// 允许 controller 对 pods 进行增删改查
// +kubebuilder:rbac:groups="",resources=pods,verbes=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods/status,verbes=get
// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the TrainingJob object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.22.4/pkg/reconcile
func (r *TrainingJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := logf.FromContext(ctx) //获取带有上下文的 logger（打印出来的日志带有 ReconcileID等元数据，方便在并发日志中追踪）

	// 根据名称获取 TrainingJob 对象
	var job batchv1.TrainingJob // 声明一个空的 TrainingJob 结构体作为查询结果的容器

	// 核心查询：根据请求中的 NamespacedName 去 API Server 拉取最新的 TrainingJob对象
	// req.NamespacedName 包含 {Namespace: "default", Name: "xxx"}
	if err := r.Get(ctx, req.NamespacedName, &job); err != nil { //
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	l.Info("发现 TrainingJob，开始处理...", "JobName", job.Name)

	//检查这个 job 下面是否有 pod
	var childPod corev1.Pod                                                   //准备记录 Pod 的表格
	podName := types.NamespacedName{Namespace: job.Namespace, Name: job.Name} //要找的 pod 的 namespace 和 name

	//尝试让 API server 查询这个 Pod,
	err := r.Get(ctx, podName, &childPod)

	// 当 Pod 已存在
	if err == nil {
		//状态同步
		podPhase := string(childPod.Status.Phase)
		if job.Status.State != podPhase {
			job.Status.State = podPhase
			if err := r.Status().Update(ctx, &job); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// 当 Pod 不存在，需要进行调度（有位置才能执行）
	if client.IgnoreNotFound(err) != nil { //有别的错误
		return ctrl.Result{}, err
	}

	l.Info("正在检查集群资源配额...", "Job", job.Name)

	//列出当前所有的 TrainingJob
	var jobList batchv1.TrainingJobList
	if err := r.List(ctx, &jobList); err != nil {
		return ctrl.Result{}, nil
	}
	// 统计运行中的 pod
	activeJob := 0
	for _, pod := range jobList.Items {
		if pod.Status.State == "Running" || pod.Status.State == "Pending" {
			activeJob++
		}
	}

	//设定阈值-集群中显卡数量
	const MaxParallelJobs = 2
	if activeJob >= MaxParallelJobs {
		l.Info("集群资源已满，无法调度", "Running", activeJob, "Max", MaxParallelJobs)

		//更新当前 pod 状态为 queued
		if job.Status.State != "Queued" {
			job.Status.State = "Queued"
			if err := r.Status().Update(ctx, &job); err != nil {
				return ctrl.Result{}, err
			}
		}
		// 该 pod 每隔 10s 唤醒一次检查是否有空位
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	//  资源充足，创建 Pod
	l.Info("资源充足，开始调度", "Running", activeJob)

	// 创建 Pod
	newPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      job.Name,
			Namespace: job.Namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    "main-container",
					Image:   job.Spec.Image,
					Command: []string{"sleep", "60"},
				},
			},
			RestartPolicy: corev1.RestartPolicyOnFailure,
		},
	}
	if err := ctrl.SetControllerReference(&job, newPod, r.Scheme); err != nil {
		return ctrl.Result{}, nil
	}

	if err := r.Create(ctx, newPod); err != nil {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TrainingJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&batchv1.TrainingJob{}).
		Owns(&corev1.Pod{}).
		Named("trainingjob").
		Complete(r)
}
