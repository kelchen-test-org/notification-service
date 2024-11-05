/*
Copyright 2024.

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
	"time"

	"github.com/konflux-ci/operator-toolkit/metadata"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	v1 "knative.dev/pkg/apis/duck/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var _ = Describe("NotificationService Controller", func() {
	var (
		pushPipelineRun, pullRequestPipelineRun            *tektonv1.PipelineRun
		notificationsCounter, notificationsFailuresCounter float64
	)
	const (
		timeout                    = time.Second * 10
		interval                   = time.Millisecond * 250
		pushPipelineRunName        = "push-pipelinerun-sample"
		pullRequestPipelineRunName = "pull-request-pipelinerun-sample"
		namespace                  = "default"
	)

	pushPipelineRunLookupKey := types.NamespacedName{Name: pushPipelineRunName, Namespace: namespace}
	pullRequestPipelineLookupKey := types.NamespacedName{Name: pullRequestPipelineRunName, Namespace: namespace}
	createdPipelineRun := &tektonv1.PipelineRun{}

	Describe("Testing successful reconcile push pipelinerun", func() {
		BeforeEach(func() {
			// Create a push pipelinerun with Unknown status (not ended)
			pushPipelineRun = &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pushPipelineRunName,
					Namespace: namespace,
					Labels: map[string]string{
						PipelineRunTypeLabel:                 PushPipelineRunTypeValue,
						"pipelines.openshift.io/used-by":     "build-cloud",
						"pipelines.openshift.io/runtime":     "nodejs",
						"pipelines.openshift.io/strategy":    "s2i",
						"appstudio.openshift.io/component":   "component-sample",
						"appstudio.openshift.io/application": "aaa",
					},
				},
				Spec: tektonv1.PipelineRunSpec{
					PipelineRef: &tektonv1.PipelineRef{},
				},
				Status: tektonv1.PipelineRunStatus{
					PipelineRunStatusFields: tektonv1.PipelineRunStatusFields{
						StartTime:      &metav1.Time{Time: time.Now()},
						CompletionTime: &metav1.Time{Time: time.Now().Add(5 * time.Minute)},
					},
					Status: v1.Status{
						Conditions: v1.Conditions{
							apis.Condition{
								Message: "Tasks Completed: 3 (Failed: 0, Cancelled 0), Incomplete: 10, Skipped:1",
								Reason:  "Running",
								Status:  "Unknown",
								Type:    apis.ConditionSucceeded,
							},
						},
					},
				},
			}
			err := k8sClient.Create(ctx, pushPipelineRun)
			Expect(err).NotTo(HaveOccurred(), "failed to create test Pipelinerun resource")
			// Wait for the resource to be created
			Eventually(func() bool {
				err := k8sClient.Get(ctx, pushPipelineRunLookupKey, createdPipelineRun)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			notificationsCounter = testutil.ToFloat64(notifications)
			notificationsFailuresCounter = testutil.ToFloat64(notificationsFailures)
		})
		Context("when a push pipelinerun is created and end successfully", func() {
			It("should reconcile successfully - Add finalizer, Read the results, add annotation and remove the finalizer, update metrics",
				func() {
					By("Creating a new push pipelinerun and add finalizer")
					err := nsr.SetupWithManager(k8sManager)
					Expect(err).ToNot(HaveOccurred())
					// The pipelinerun should be reconciled and the notification finalizer has been added successfully
					Eventually(func() bool {
						err := k8sClient.Get(ctx, pushPipelineRunLookupKey, createdPipelineRun)
						Expect(err).ToNot(HaveOccurred())
						return controllerutil.ContainsFinalizer(createdPipelineRun, NotificationPipelineRunFinalizer)
					}, timeout, interval).Should(BeTrue())
					Expect(controllerutil.ContainsFinalizer(createdPipelineRun, NotificationPipelineRunFinalizer)).To(BeTrue())
					// Check the Notify was not called
					Expect(mn.Counter).To(BeZero())

					By("Updating status to completed successfully")
					createdPipelineRun.Status = tektonv1.PipelineRunStatus{
						PipelineRunStatusFields: tektonv1.PipelineRunStatusFields{
							StartTime:      &metav1.Time{Time: time.Now()},
							CompletionTime: &metav1.Time{Time: time.Now().Add(5 * time.Minute)},
							Results: []tektonv1.PipelineRunResult{
								{
									Name:  "IMAGE_DIGEST",
									Value: *tektonv1.NewStructuredValues("image_digest_value"),
								},
								{
									Name:  "IMAGE_URL",
									Value: *tektonv1.NewStructuredValues("image"),
								},
								{
									Name:  "CHAINS-GIT_URL",
									Value: *tektonv1.NewStructuredValues("git_url_value"),
								},
								{
									Name:  "CHAINS-GIT_COMMIT",
									Value: *tektonv1.NewStructuredValues("git_commit_value"),
								},
							},
						},
						Status: v1.Status{
							Conditions: v1.Conditions{
								apis.Condition{
									Message: "Tasks Completed: 12 (Failed: 0, Cancelled 0), Skipped: 2",
									Reason:  "Completed",
									Status:  "True",
									Type:    apis.ConditionSucceeded,
								},
							},
						},
					}
					Expect(k8sClient.Status().Update(ctx, createdPipelineRun)).Should(Succeed())
					// The pipelinerun should be reconciled:
					// Read the results, add the notification annotation, remove the finalizer
					Eventually(func() bool {
						err := k8sClient.Get(ctx, pushPipelineRunLookupKey, createdPipelineRun)
						Expect(err).ToNot(HaveOccurred())
						return metadata.HasAnnotationWithValue(createdPipelineRun, NotificationPipelineRunAnnotation, NotificationPipelineRunAnnotationValue)
					}, timeout, interval).Should(BeTrue())
					Expect(controllerutil.ContainsFinalizer(createdPipelineRun, NotificationPipelineRunFinalizer)).To(BeFalse())
					// Check the Notify was called only once
					Expect(mn.Counter).To(Equal(1))
					// Check that notifications metric increased by 1
					Expect(testutil.ToFloat64(notifications)).To(Equal(notificationsCounter + 1))
					// Check that notificationsFailures metric did not change
					Expect(testutil.ToFloat64(notificationsFailures)).To(Equal(notificationsFailuresCounter))
				})
		})

		Context("when a push pipelinerun is created and deleted while in running state", func() {
			It("should reconcile successfully - Add finalizer when created and remove it when deleted and still running",
				func() {
					By("Creating a new push pipelinerun and add finalizer")
					err := nsr.SetupWithManager(k8sManager)
					Expect(err).ToNot(HaveOccurred())
					// The pipelinerun should be reconciled and the notification finalizer has been added successfully
					Eventually(func() bool {
						err := k8sClient.Get(ctx, pushPipelineRunLookupKey, createdPipelineRun)
						Expect(err).ToNot(HaveOccurred())
						return controllerutil.ContainsFinalizer(createdPipelineRun, NotificationPipelineRunFinalizer)
					}, timeout, interval).Should(BeTrue())
					Expect(controllerutil.ContainsFinalizer(createdPipelineRun, NotificationPipelineRunFinalizer)).To(BeTrue())
					// Check the Notify was not called
					Expect(mn.Counter).To(BeZero())

					By("Deleting the pipelinerun")
					// Since DeletionTimestamp field is immutable, it is impossible to update the resource.
					// instead, we will delete the pipelinerun and make sure it is deleted successfully
					// if so, then the finalizer was removed successfully
					Expect(k8sClient.Delete(ctx, createdPipelineRun)).To(Succeed())

					// The pipelinerun should be reconciled, the finalizer gets removed and the pipelinerun is deleted
					Eventually(func() bool {
						err := k8sClient.Get(ctx, pushPipelineRunLookupKey, createdPipelineRun)
						return err != nil
					}, timeout, interval).Should(BeTrue())
					// Expect(controllerutil.ContainsFinalizer(createdPipelineRun, NotificationPipelineRunFinalizer)).To(BeFalse())
					// Check the Notify was called only once
					Expect(mn.Counter).To(BeZero())
					// Check that notifications metric was not increased
					Expect(testutil.ToFloat64(notifications)).To(Equal(notificationsCounter))
					// Check that notificationsFailures metric did not change
					Expect(testutil.ToFloat64(notificationsFailures)).To(Equal(notificationsFailuresCounter))
				})
		})

		Context("when a push pipelinerun is created and end successfully, but Notify fails", func() {
			It("should reconcile successfully - Add finalizer, Fail sending results, Not add annotation, Update metrics",
				func() {
					By("Creating a new push pipelinerun and add finalizer")
					err := fakeErrorNotifyNsr.SetupWithManager(k8sManager)
					Expect(err).ToNot(HaveOccurred())
					// The pipelinerun should be reconciled and the notification finalizer has been added successfully
					Eventually(func() bool {
						err := k8sClient.Get(ctx, pushPipelineRunLookupKey, createdPipelineRun)
						Expect(err).ToNot(HaveOccurred())
						return controllerutil.ContainsFinalizer(createdPipelineRun, NotificationPipelineRunFinalizer)
					}, timeout, interval).Should(BeTrue())
					Expect(controllerutil.ContainsFinalizer(createdPipelineRun, NotificationPipelineRunFinalizer)).To(BeTrue())
					// Check the Notify was not called
					Expect(mn.Counter).To(BeZero())

					By("Updating status to completed successfully")
					createdPipelineRun.Status = tektonv1.PipelineRunStatus{
						PipelineRunStatusFields: tektonv1.PipelineRunStatusFields{
							StartTime:      &metav1.Time{Time: time.Now()},
							CompletionTime: &metav1.Time{Time: time.Now().Add(5 * time.Minute)},
							Results: []tektonv1.PipelineRunResult{
								{
									Name:  "IMAGE_DIGEST",
									Value: *tektonv1.NewStructuredValues("image_digest_value"),
								},
								{
									Name:  "IMAGE_URL",
									Value: *tektonv1.NewStructuredValues("image"),
								},
								{
									Name:  "CHAINS-GIT_URL",
									Value: *tektonv1.NewStructuredValues("git_url_value"),
								},
								{
									Name:  "CHAINS-GIT_COMMIT",
									Value: *tektonv1.NewStructuredValues("git_commit_value"),
								},
							},
						},
						Status: v1.Status{
							Conditions: v1.Conditions{
								apis.Condition{
									Message: "Tasks Completed: 12 (Failed: 0, Cancelled 0), Skipped: 2",
									Reason:  "Completed",
									Status:  "True",
									Type:    apis.ConditionSucceeded,
								},
							},
						},
					}
					Expect(k8sClient.Status().Update(ctx, createdPipelineRun)).Should(Succeed())
					// Wait for the Notify method to be called
					Eventually(func() bool {
						return fakeErrorNotify.Counter != 0
					}, timeout, interval).Should(BeTrue())
					Expect(controllerutil.ContainsFinalizer(createdPipelineRun, NotificationPipelineRunFinalizer)).To(BeTrue())
					Expect(metadata.HasAnnotationWithValue(createdPipelineRun, NotificationPipelineRunAnnotation, NotificationPipelineRunAnnotationValue)).To(BeFalse())
					// Since returning an error will reconcile the resource indefinitely, and we cannot track the number of Notify calls and
					// the number of times the metrics will be updated we can check that the notification and notificationFailures
					// metrics after at least one failure were increased
					Expect(testutil.ToFloat64(notifications)).To(BeNumerically(">", notificationsCounter))
					Expect(testutil.ToFloat64(notificationsFailures)).To(BeNumerically(">", notificationsFailuresCounter))
				})
		})
		Context("when a push pipelinerun is created and end with failure", func() {
			It("should reconcile successfully - Add finalizer, Not reading the results, Not adding annotation and remove the finalizer, Not update metrics",
				func() {
					By("Creating a new push pipelinerun and add finalizer")
					err := nsr.SetupWithManager(k8sManager)
					Expect(err).ToNot(HaveOccurred())
					// The pipelinerun should be reconciled and the notification finalizer has been added successfully
					Eventually(func() bool {
						err := k8sClient.Get(ctx, pushPipelineRunLookupKey, createdPipelineRun)
						Expect(err).ToNot(HaveOccurred())
						return controllerutil.ContainsFinalizer(createdPipelineRun, NotificationPipelineRunFinalizer)
					}, timeout, interval).Should(BeTrue())
					Expect(controllerutil.ContainsFinalizer(createdPipelineRun, NotificationPipelineRunFinalizer)).To(BeTrue())

					By("Updating status to completed with failure")
					createdPipelineRun.Status = tektonv1.PipelineRunStatus{
						PipelineRunStatusFields: tektonv1.PipelineRunStatusFields{
							StartTime:      &metav1.Time{Time: time.Now()},
							CompletionTime: &metav1.Time{Time: time.Now().Add(5 * time.Minute)},
						},
						Status: v1.Status{
							Conditions: v1.Conditions{
								apis.Condition{
									Message: "Tasks Completed: 12 (Failed: 0, Cancelled 0), Skipped: 2",
									Reason:  "CouldntGetTask",
									Status:  "False",
									Type:    apis.ConditionSucceeded,
								},
							},
						},
					}
					Expect(k8sClient.Status().Update(ctx, createdPipelineRun)).Should(Succeed())

					// The pipelinerun should be reconciled:
					// Remove the finalizer
					Eventually(func() bool {
						err := k8sClient.Get(ctx, pushPipelineRunLookupKey, createdPipelineRun)
						Expect(err).ToNot(HaveOccurred())
						return controllerutil.ContainsFinalizer(createdPipelineRun, NotificationPipelineRunFinalizer)
					}, timeout, interval).Should(BeFalse())
					Expect(metadata.HasAnnotationWithValue(createdPipelineRun, NotificationPipelineRunAnnotation, NotificationPipelineRunAnnotationValue)).To(BeFalse())
					// Check the Notify was not called
					Expect(mn.Counter).To(BeZero())
					Expect(testutil.ToFloat64(notifications)).To(Equal(notificationsCounter))
					Expect(testutil.ToFloat64(notificationsFailures)).To(Equal(notificationsFailuresCounter))
				})
		})
	})

	Describe("Testing No reconcile with non push pipelinerun", func() {
		Context("When a non push pipelineRun is created", func() {
			It("Reconcile should not run", func() {
				err := nsr.SetupWithManager(k8sManager)
				Expect(err).ToNot(HaveOccurred())
				// Create a pull_request pipelinerun
				pullRequestPipelineRun = &tektonv1.PipelineRun{
					ObjectMeta: metav1.ObjectMeta{
						Name:      pullRequestPipelineRunName,
						Namespace: namespace,
						Labels: map[string]string{
							PipelineRunTypeLabel:                 "pull_request",
							"pipelines.openshift.io/used-by":     "build-cloud",
							"pipelines.openshift.io/runtime":     "nodejs",
							"pipelines.openshift.io/strategy":    "s2i",
							"appstudio.openshift.io/component":   "component-sample",
							"appstudio.openshift.io/application": "aaa",
						},
					},
					Spec: tektonv1.PipelineRunSpec{
						PipelineRef: &tektonv1.PipelineRef{},
					},
					Status: tektonv1.PipelineRunStatus{
						PipelineRunStatusFields: tektonv1.PipelineRunStatusFields{
							StartTime:      &metav1.Time{Time: time.Now()},
							CompletionTime: &metav1.Time{Time: time.Now().Add(5 * time.Minute)},
						},
						Status: v1.Status{
							Conditions: v1.Conditions{
								apis.Condition{
									Message: "Tasks Completed: 3 (Failed: 0, Cancelled 0), Incomplete: 10, Skipped:1",
									Reason:  "Running",
									Status:  "Unknown",
									Type:    apis.ConditionSucceeded,
								},
							},
						},
					},
				}
				err = k8sClient.Create(ctx, pullRequestPipelineRun)
				Expect(err).NotTo(HaveOccurred(), "failed to create test Pipelinerun resource")

				// Wait for the resource to be created
				Eventually(func() bool {
					err := k8sClient.Get(ctx, pullRequestPipelineLookupKey, createdPipelineRun)
					return err == nil
				}, timeout, interval).Should(BeTrue())

				// No finalizer should be added
				Expect(controllerutil.ContainsFinalizer(createdPipelineRun, NotificationPipelineRunFinalizer)).To(BeFalse())

				By("Updating status to completed successfully")
				createdPipelineRun.Status = tektonv1.PipelineRunStatus{
					PipelineRunStatusFields: tektonv1.PipelineRunStatusFields{
						StartTime:      &metav1.Time{Time: time.Now()},
						CompletionTime: &metav1.Time{Time: time.Now().Add(5 * time.Minute)},
						Results: []tektonv1.PipelineRunResult{
							{
								Name:  "IMAGE_DIGEST",
								Value: *tektonv1.NewStructuredValues("image_digest_value"),
							},
							{
								Name:  "IMAGE_URL",
								Value: *tektonv1.NewStructuredValues("image"),
							},
							{
								Name:  "CHAINS-GIT_URL",
								Value: *tektonv1.NewStructuredValues("git_url_value"),
							},
							{
								Name:  "CHAINS-GIT_COMMIT",
								Value: *tektonv1.NewStructuredValues("git_commit_value"),
							},
						},
					},
					Status: v1.Status{
						Conditions: v1.Conditions{
							apis.Condition{
								Message: "Tasks Completed: 12 (Failed: 0, Cancelled 0), Skipped: 2",
								Reason:  "Completed",
								Status:  "True",
								Type:    apis.ConditionSucceeded,
							},
						},
					},
				}
				Expect(k8sClient.Status().Update(ctx, createdPipelineRun)).Should(Succeed())
				Eventually(func() bool {
					err := k8sClient.Get(ctx, pullRequestPipelineLookupKey, createdPipelineRun)
					return err == nil
				}, timeout, interval).Should(BeTrue())
				// Annotation and finalizer should not be added
				Expect(controllerutil.ContainsFinalizer(createdPipelineRun, NotificationPipelineRunFinalizer)).To(BeFalse())
				Expect(metadata.HasAnnotationWithValue(createdPipelineRun, NotificationPipelineRunAnnotation, NotificationPipelineRunAnnotationValue)).To(BeFalse())
				// Check the Notify was not called
				Expect(mn.Counter).To(BeZero())
				Expect(testutil.ToFloat64(notifications)).To(Equal(notificationsCounter))
				Expect(testutil.ToFloat64(notificationsFailures)).To(Equal(notificationsFailuresCounter))
			})
		})
	})
	AfterEach(func() {
		err := k8sClient.Delete(ctx, createdPipelineRun)
		Expect(err == nil || errors.IsNotFound(err)).To(BeTrue())
	})
})
