package controllers

import (
	"testing"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	"github.com/metal3-io/baremetal-operator/pkg/bmc"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	"github.com/metal3-io/baremetal-operator/pkg/provisioner"
	promutil "github.com/prometheus/client_golang/prometheus/testutil"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

func testStateMachine(host *metal3v1alpha1.BareMetalHost) *hostStateMachine {
	r := newTestReconciler()
	p, _ := r.ProvisionerFactory(*host.DeepCopy(), bmc.Credentials{},
		func(reason, message string) {})
	return newHostStateMachine(host, r, p, true)
}

func TestProvisioningCapacity(t *testing.T) {
	testCases := []struct {
		Scenario string

		HasProvisioningCapacity bool
		Host                    *metal3v1alpha1.BareMetalHost

		ExpectedProvisioningState metal3v1alpha1.ProvisioningState
		ExpectedDelayed           bool
	}{
		{
			Scenario:                "transition-to-inspecting-delayed",
			Host:                    host(metal3v1alpha1.StateRegistering).build(),
			HasProvisioningCapacity: false,

			ExpectedProvisioningState: metal3v1alpha1.StateRegistering,
			ExpectedDelayed:           true,
		},
		{
			Scenario:                "transition-to-provisioning-delayed",
			Host:                    host(metal3v1alpha1.StateReady).build(),
			HasProvisioningCapacity: false,

			ExpectedProvisioningState: metal3v1alpha1.StateReady,
			ExpectedDelayed:           true,
		},
		{
			Scenario:                "transition-to-inspecting-ok",
			Host:                    host(metal3v1alpha1.StateRegistering).build(),
			HasProvisioningCapacity: true,

			ExpectedProvisioningState: metal3v1alpha1.StateInspecting,
			ExpectedDelayed:           false,
		},
		{
			Scenario:                "transition-to-provisioning-ok",
			Host:                    host(metal3v1alpha1.StateReady).build(),
			HasProvisioningCapacity: true,

			ExpectedProvisioningState: metal3v1alpha1.StateProvisioning,
			ExpectedDelayed:           false,
		},

		{
			Scenario:                "already-delayed-delayed",
			Host:                    host(metal3v1alpha1.StateReady).SetOperationalStatus(metal3v1alpha1.OperationalStatusDelayed).build(),
			HasProvisioningCapacity: false,

			ExpectedProvisioningState: metal3v1alpha1.StateReady,
			ExpectedDelayed:           true,
		},
		{
			Scenario:                "already-delayed-ok",
			Host:                    host(metal3v1alpha1.StateReady).SetOperationalStatus(metal3v1alpha1.OperationalStatusDelayed).build(),
			HasProvisioningCapacity: true,

			ExpectedProvisioningState: metal3v1alpha1.StateReady,
			ExpectedDelayed:           false,
		},

		{
			Scenario:                "untracked-inspecting-delayed",
			Host:                    host(metal3v1alpha1.StateInspecting).build(),
			HasProvisioningCapacity: false,

			ExpectedProvisioningState: metal3v1alpha1.StateInspecting,
			ExpectedDelayed:           true,
		},
		{
			Scenario:                "untracked-inspecting-ok",
			Host:                    host(metal3v1alpha1.StateInspecting).build(),
			HasProvisioningCapacity: true,

			ExpectedProvisioningState: metal3v1alpha1.StateMatchProfile,
			ExpectedDelayed:           false,
		},
		{
			Scenario:                "untracked-inspecting-delayed",
			Host:                    host(metal3v1alpha1.StateProvisioning).build(),
			HasProvisioningCapacity: false,

			ExpectedProvisioningState: metal3v1alpha1.StateProvisioning,
			ExpectedDelayed:           true,
		},
		{
			Scenario:                "untracked-provisioning-ok",
			Host:                    host(metal3v1alpha1.StateProvisioning).build(),
			HasProvisioningCapacity: true,

			ExpectedProvisioningState: metal3v1alpha1.StateProvisioned,
			ExpectedDelayed:           false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Scenario, func(t *testing.T) {
			prov := newMockProvisioner()
			prov.setHasProvisioningCapacity(tc.HasProvisioningCapacity)
			hsm := newHostStateMachine(tc.Host, &BareMetalHostReconciler{}, prov, true)
			info := makeDefaultReconcileInfo(tc.Host)
			delayedProvisioningHostCounters.Reset()

			result := hsm.ReconcileState(info)

			assert.Equal(t, tc.ExpectedProvisioningState, tc.Host.Status.Provisioning.State)
			assert.Equal(t, tc.ExpectedDelayed, metal3v1alpha1.OperationalStatusDelayed == tc.Host.Status.OperationalStatus, "Expected OperationalStatusDelayed")
			assert.Equal(t, tc.ExpectedDelayed, assert.ObjectsAreEqual(actionDelayed{}, result), "Expected actionDelayed")

			if tc.ExpectedDelayed {
				counter, _ := delayedProvisioningHostCounters.GetMetricWith(hostMetricLabels(info.request))
				initialCounterValue := promutil.ToFloat64(counter)
				for _, sb := range info.postSaveCallbacks {
					sb()
				}
				assert.Greater(t, promutil.ToFloat64(counter), initialCounterValue)
			}
		})
	}
}

func TestProvisioningCancelled(t *testing.T) {
	testCases := []struct {
		Scenario string
		Host     metal3v1alpha1.BareMetalHost
		Expected bool
	}{
		{
			Scenario: "with image url, unprovisioned",
			Host: metal3v1alpha1.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "myhost",
					Namespace: "myns",
				},
				Spec: metal3v1alpha1.BareMetalHostSpec{
					Image: &metal3v1alpha1.Image{
						URL: "not-empty",
					},
					Online: true,
				},
			},
			Expected: false,
		},

		{
			Scenario: "with image, unprovisioned",
			Host: metal3v1alpha1.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "myhost",
					Namespace: "myns",
				},
				Spec: metal3v1alpha1.BareMetalHostSpec{
					Image:  &metal3v1alpha1.Image{},
					Online: true,
				},
			},
			Expected: true,
		},

		{
			Scenario: "without, unprovisioned",
			Host: metal3v1alpha1.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "myhost",
					Namespace: "myns",
				},
				Spec: metal3v1alpha1.BareMetalHostSpec{
					Online: true,
				},
			},
			Expected: true,
		},

		{
			Scenario: "with image url, offline",
			Host: metal3v1alpha1.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "myhost",
					Namespace: "myns",
				},
				Spec: metal3v1alpha1.BareMetalHostSpec{
					Image: &metal3v1alpha1.Image{
						URL: "not-empty",
					},
					Online: false,
				},
			},
			Expected: false,
		},

		{
			Scenario: "provisioned",
			Host: metal3v1alpha1.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "myhost",
					Namespace: "myns",
				},
				Spec: metal3v1alpha1.BareMetalHostSpec{
					Image: &metal3v1alpha1.Image{
						URL: "same",
					},
					Online: true,
				},
				Status: metal3v1alpha1.BareMetalHostStatus{
					Provisioning: metal3v1alpha1.ProvisionStatus{
						Image: metal3v1alpha1.Image{
							URL: "same",
						},
					},
				},
			},
			Expected: false,
		},

		{
			Scenario: "removed image",
			Host: metal3v1alpha1.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "myhost",
					Namespace: "myns",
				},
				Spec: metal3v1alpha1.BareMetalHostSpec{
					Online: true,
				},
				Status: metal3v1alpha1.BareMetalHostStatus{
					Provisioning: metal3v1alpha1.ProvisionStatus{
						Image: metal3v1alpha1.Image{
							URL: "same",
						},
					},
				},
			},
			Expected: true,
		},

		{
			Scenario: "changed image",
			Host: metal3v1alpha1.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "myhost",
					Namespace: "myns",
				},
				Spec: metal3v1alpha1.BareMetalHostSpec{
					Image: &metal3v1alpha1.Image{
						URL: "not-empty",
					},
					Online: true,
				},
				Status: metal3v1alpha1.BareMetalHostStatus{
					Provisioning: metal3v1alpha1.ProvisionStatus{
						Image: metal3v1alpha1.Image{
							URL: "also-not-empty",
						},
					},
				},
			},
			Expected: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.Scenario, func(t *testing.T) {
			hsm := testStateMachine(&tc.Host)
			actual := hsm.provisioningCancelled()
			if tc.Expected && !actual {
				t.Error("expected to need deprovisioning")
			}
			if !tc.Expected && actual {
				t.Error("did not expect to need deprovisioning")
			}
		})
	}
}

func TestErrorCountIncreasedWhenProvisionerFails(t *testing.T) {

	tests := []struct {
		Scenario string
		Host     *metal3v1alpha1.BareMetalHost
	}{
		{
			Scenario: "inspecting",
			Host:     host(metal3v1alpha1.StateInspecting).build(),
		},
		{
			Scenario: "ready",
			Host:     host(metal3v1alpha1.StateReady).build(),
		},
		{
			Scenario: "deprovisioning",
			Host:     host(metal3v1alpha1.StateDeprovisioning).build(),
		},
		{
			Scenario: "provisioning",
			Host:     host(metal3v1alpha1.StateProvisioning).SetImageURL("imageSpecUrl").build(),
		},
		{
			Scenario: "externallyProvisioned",
			Host:     host(metal3v1alpha1.StateExternallyProvisioned).SetExternallyProvisioned().build(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.Scenario, func(t *testing.T) {
			prov := newMockProvisioner()
			hsm := newHostStateMachine(tt.Host, &BareMetalHostReconciler{}, prov, true)
			info := makeDefaultReconcileInfo(tt.Host)

			prov.setNextError("some error")
			result := hsm.ReconcileState(info)

			assert.Greater(t, tt.Host.Status.ErrorCount, 0)
			assert.True(t, result.Dirty())
		})
	}
}

func TestErrorCountIncreasedWhenRegistrationFails(t *testing.T) {
	bmh := host(metal3v1alpha1.StateRegistering).build()
	prov := newMockProvisioner()
	hsm := newHostStateMachine(bmh, &BareMetalHostReconciler{}, prov, true)
	info := makeDefaultReconcileInfo(bmh)
	bmh.Status.GoodCredentials = metal3v1alpha1.CredentialsStatus{}

	prov.setNextError("some error")
	result := hsm.ReconcileState(info)

	assert.Greater(t, bmh.Status.ErrorCount, 0)
	assert.True(t, result.Dirty())
}

func TestErrorCountCleared(t *testing.T) {

	tests := []struct {
		Scenario                     string
		Host                         *metal3v1alpha1.BareMetalHost
		PreserveErrorCountOnComplete bool
	}{
		{
			Scenario: "registering",
			Host:     host(metal3v1alpha1.StateRegistering).build(),
		},
		{
			Scenario: "inspecting",
			Host:     host(metal3v1alpha1.StateInspecting).build(),
		},
		{
			Scenario:                     "ready",
			Host:                         host(metal3v1alpha1.StateReady).build(),
			PreserveErrorCountOnComplete: true,
		},
		{
			Scenario: "deprovisioning",
			Host:     host(metal3v1alpha1.StateDeprovisioning).build(),
		},
		{
			Scenario: "provisioning",
			Host:     host(metal3v1alpha1.StateProvisioning).SetImageURL("imageSpecUrl").build(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.Scenario, func(t *testing.T) {
			prov := newMockProvisioner()
			hsm := newHostStateMachine(tt.Host, &BareMetalHostReconciler{}, prov, true)
			info := makeDefaultReconcileInfo(tt.Host)

			info.host.Status.ErrorCount = 1
			prov.setNextResult(true)
			result := hsm.ReconcileState(info)

			assert.Equal(t, 1, tt.Host.Status.ErrorCount)
			assert.True(t, result.Dirty())

			prov.setNextResult(false)
			hsm.ReconcileState(info)
			if tt.PreserveErrorCountOnComplete {
				assert.Equal(t, 1, tt.Host.Status.ErrorCount)
			} else {
				assert.Equal(t, 0, tt.Host.Status.ErrorCount)
			}
		})
	}
}

func TestErrorClean(t *testing.T) {

	tests := []struct {
		Scenario    string
		Host        *metal3v1alpha1.BareMetalHost
		SecretName  string
		ExpectError bool
	}{
		{
			Scenario: "clean-after-registration-error",
			Host: host(metal3v1alpha1.StateInspecting).
				SetStatusError(metal3v1alpha1.OperationalStatusError, metal3v1alpha1.RegistrationError, "some error", 1).
				build(),
		},
		{
			Scenario: "not-clean-after-provisioned-registration-error",
			Host: host(metal3v1alpha1.StateInspecting).
				SetStatusError(metal3v1alpha1.OperationalStatusError, metal3v1alpha1.ProvisionedRegistrationError, "some error", 1).
				build(),
			ExpectError: true,
		},
		{
			Scenario: "clean-after-creds-change",
			Host: host(metal3v1alpha1.StateReady).
				SetStatusError(metal3v1alpha1.OperationalStatusError, metal3v1alpha1.InspectionError, "some error", 1).
				build(),
			SecretName: "NewCreds",
		},
	}
	for _, tt := range tests {
		t.Run(tt.Scenario, func(t *testing.T) {
			prov := &mockProvisioner{}
			hsm := newHostStateMachine(tt.Host, &BareMetalHostReconciler{}, prov, true)

			info := makeDefaultReconcileInfo(tt.Host)
			if tt.SecretName != "" {
				info.bmcCredsSecret.Name = tt.SecretName
			}

			hsm.ReconcileState(info)

			if tt.ExpectError {
				assert.Equal(t, tt.Host.Status.ErrorType, v1alpha1.ProvisionedRegistrationError)
				assert.NotEmpty(t, tt.Host.Status.ErrorMessage)
			} else {
				assert.Equal(t, tt.Host.Status.OperationalStatus, v1alpha1.OperationalStatusOK)
				assert.Empty(t, tt.Host.Status.ErrorType)
				assert.Empty(t, tt.Host.Status.ErrorMessage)
			}
		})
	}
}

type hostBuilder struct {
	metal3v1alpha1.BareMetalHost
}

func host(state metal3v1alpha1.ProvisioningState) *hostBuilder {

	creds := metal3v1alpha1.CredentialsStatus{
		Reference: &corev1.SecretReference{
			Name:      "secretRefName",
			Namespace: "secretNs",
		},
		Version: "100",
	}

	return &hostBuilder{
		metal3v1alpha1.BareMetalHost{
			Spec: v1alpha1.BareMetalHostSpec{
				Online: true,
				Image: &v1alpha1.Image{
					URL: "not-empty",
				},
				RootDeviceHints: &v1alpha1.RootDeviceHints{},
			},
			Status: metal3v1alpha1.BareMetalHostStatus{
				Provisioning: metal3v1alpha1.ProvisionStatus{
					State:    state,
					BootMode: v1alpha1.DefaultBootMode,
					Image: v1alpha1.Image{
						URL: "", //needs provisioning
					},
				},
				GoodCredentials:   creds,
				TriedCredentials:  creds,
				OperationalStatus: metal3v1alpha1.OperationalStatusOK,
			},
		},
	}
}

func (hb *hostBuilder) build() *metal3v1alpha1.BareMetalHost {
	return &hb.BareMetalHost
}

func (hb *hostBuilder) SetTriedCredentials() *hostBuilder {
	hb.Status.TriedCredentials = hb.Status.GoodCredentials
	return hb
}

func (hb *hostBuilder) SetExternallyProvisioned() *hostBuilder {
	hb.Spec.ExternallyProvisioned = true
	return hb
}

func (hb *hostBuilder) SetImageURL(url string) *hostBuilder {
	hb.Spec.Image = &metal3v1alpha1.Image{
		URL: url,
	}
	return hb
}

func (hb *hostBuilder) SetStatusError(opStatus metal3v1alpha1.OperationalStatus, errType metal3v1alpha1.ErrorType, errMsg string, errCount int) *hostBuilder {
	hb.Status.OperationalStatus = opStatus
	hb.Status.ErrorType = errType
	hb.Status.ErrorMessage = errMsg
	hb.Status.ErrorCount = errCount

	return hb
}

func (hb *hostBuilder) SetOperationalStatus(status metal3v1alpha1.OperationalStatus) *hostBuilder {
	hb.Status.OperationalStatus = status
	return hb
}

func makeDefaultReconcileInfo(host *metal3v1alpha1.BareMetalHost) *reconcileInfo {
	return &reconcileInfo{
		log:     logf.Log.WithName("controllers").WithName("BareMetalHost").WithName("host_state_machine"),
		host:    host,
		request: ctrl.Request{},
		bmcCredsSecret: &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:            host.Status.GoodCredentials.Reference.Name,
				Namespace:       host.Status.GoodCredentials.Reference.Namespace,
				ResourceVersion: host.Status.GoodCredentials.Version,
			},
		},
	}
}

func newMockProvisioner() *mockProvisioner {
	return &mockProvisioner{
		hasProvisioningCapacity: true,
	}
}

type mockProvisioner struct {
	hasProvisioningCapacity bool
	nextResult              provisioner.Result
}

func (m *mockProvisioner) setHasProvisioningCapacity(hasCapacity bool) {
	m.hasProvisioningCapacity = hasCapacity
}

func (m *mockProvisioner) HasProvisioningCapacity() (result bool, err error) {
	return m.hasProvisioningCapacity, nil
}

func (m *mockProvisioner) setNextError(msg string) {
	m.nextResult = provisioner.Result{
		ErrorMessage: msg,
	}
}

func (m *mockProvisioner) setNextResult(dirty bool) {
	m.nextResult = provisioner.Result{
		Dirty:        dirty,
		ErrorMessage: "",
	}
}

func (m *mockProvisioner) ValidateManagementAccess(credentialsChanged, force bool) (result provisioner.Result, provID string, err error) {
	return m.nextResult, "", err
}

func (m *mockProvisioner) InspectHardware(force bool) (result provisioner.Result, details *metal3v1alpha1.HardwareDetails, err error) {
	details = &metal3v1alpha1.HardwareDetails{}
	return m.nextResult, details, err
}

func (m *mockProvisioner) UpdateHardwareState() (hwState provisioner.HardwareState, err error) {
	return
}

func (m *mockProvisioner) Adopt(force bool) (result provisioner.Result, err error) {
	return m.nextResult, err
}

func (m *mockProvisioner) Provision(configData provisioner.HostConfigData) (result provisioner.Result, err error) {
	return m.nextResult, err
}

func (m *mockProvisioner) Deprovision(force bool) (result provisioner.Result, err error) {
	return m.nextResult, err
}

func (m *mockProvisioner) Delete() (result provisioner.Result, err error) {
	return m.nextResult, err
}

func (m *mockProvisioner) PowerOn() (result provisioner.Result, err error) {
	return m.nextResult, err
}

func (m *mockProvisioner) PowerOff() (result provisioner.Result, err error) {
	return m.nextResult, err
}

func (m *mockProvisioner) IsReady() (result bool, err error) {
	return
}

func TestUpdateBootModeStatus(t *testing.T) {
	testCases := []struct {
		Scenario       string
		SpecValue      metal3v1alpha1.BootMode
		StatusValue    metal3v1alpha1.BootMode
		ExpectedValue  metal3v1alpha1.BootMode
		ExpectedChange bool
	}{
		{
			Scenario:       "default",
			SpecValue:      "",
			StatusValue:    "",
			ExpectedValue:  metal3v1alpha1.DefaultBootMode,
			ExpectedChange: true,
		},

		{
			Scenario:       "set UEFI",
			SpecValue:      metal3v1alpha1.UEFI,
			StatusValue:    "",
			ExpectedValue:  metal3v1alpha1.UEFI,
			ExpectedChange: true,
		},

		{
			Scenario:       "already UEFI",
			SpecValue:      metal3v1alpha1.UEFI,
			StatusValue:    metal3v1alpha1.UEFI,
			ExpectedValue:  metal3v1alpha1.UEFI,
			ExpectedChange: false,
		},

		{
			Scenario:       "set Legacy",
			SpecValue:      metal3v1alpha1.Legacy,
			StatusValue:    "",
			ExpectedValue:  metal3v1alpha1.Legacy,
			ExpectedChange: true,
		},

		{
			Scenario:       "already Legacy",
			SpecValue:      metal3v1alpha1.Legacy,
			StatusValue:    metal3v1alpha1.Legacy,
			ExpectedValue:  metal3v1alpha1.Legacy,
			ExpectedChange: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.Scenario, func(t *testing.T) {
			host := metal3v1alpha1.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "myhost",
					Namespace: "myns",
				},
				Spec: metal3v1alpha1.BareMetalHostSpec{
					Image: &metal3v1alpha1.Image{
						URL: "not-empty",
					},
					Online:   true,
					BootMode: tc.SpecValue,
				},
				Status: metal3v1alpha1.BareMetalHostStatus{
					Provisioning: metal3v1alpha1.ProvisionStatus{
						Image: metal3v1alpha1.Image{
							URL: "also-not-empty",
						},
						BootMode: tc.StatusValue,
					},
				},
			}
			changed := updateBootModeStatus(&host)
			assert.Equal(t, tc.ExpectedChange, changed, "unexpected change response")
			assert.Equal(t, tc.ExpectedValue, host.Status.Provisioning.BootMode)
		})
	}
}
