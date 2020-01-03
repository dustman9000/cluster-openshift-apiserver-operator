package apiservicecontroller

import (
	"sort"
	"strings"
	"testing"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-openshift-apiserver-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/status"
	operatorv1helpers "github.com/openshift/library-go/pkg/operator/v1helpers"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/client-go/kubernetes/fake"
	kubetesting "k8s.io/client-go/testing"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	kubeaggregatorfake "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/fake"
)

func TestAvailableStatus(t *testing.T) {
	//servicePort := utilpointer.Int32Ptr(443)
	testCases := []struct {
		name                string
		expectedStatus      operatorv1.ConditionStatus
		expectedReasons     []string
		expectedMessages    []string
		existingAPIServices []runtime.Object
		apiServiceReactor   kubetesting.ReactionFunc
		daemonReactor       kubetesting.ReactionFunc
	}{
		{
			name:           "Default",
			expectedStatus: operatorv1.ConditionTrue,
		},
		{
			name:             "APIServiceCreateFailure",
			expectedStatus:   operatorv1.ConditionFalse,
			expectedReasons:  []string{"Error"},
			expectedMessages: []string{"TEST ERROR: fail to create apiservice"},

			apiServiceReactor: func(action kubetesting.Action) (handled bool, ret runtime.Object, err error) {
				if action.GetVerb() != "create" {
					return false, nil, nil
				}
				if action.(kubetesting.CreateAction).GetObject().(*apiregistrationv1.APIService).Name == "v1.build.openshift.io" {
					return true, nil, errors.New("TEST ERROR: fail to create apiservice")
				}
				return false, nil, nil
			},
		},
		{
			name:             "APIServiceGetFailure",
			expectedStatus:   operatorv1.ConditionFalse,
			expectedReasons:  []string{"Error"},
			expectedMessages: []string{"TEST ERROR: fail to get apiservice"},

			existingAPIServices: []runtime.Object{
				runtime.Object(newAPIService("build.openshift.io", "v1")),
				runtime.Object(newAPIService("apps.openshift.io", "v1")),
			},
			apiServiceReactor: func(action kubetesting.Action) (handled bool, ret runtime.Object, err error) {
				if action.GetVerb() == "get" && action.(kubetesting.GetAction).GetName() == "v1.build.openshift.io" {
					return true, nil, errors.New("TEST ERROR: fail to get apiservice")
				}
				return false, nil, nil
			},
		},
		{
			name:             "APIServiceNotAvailable",
			expectedStatus:   operatorv1.ConditionFalse,
			expectedReasons:  []string{"Error"},
			expectedMessages: []string{"apiservices.apiregistration.k8s.io/v1.build.openshift.io: not available: TEST MESSAGE"},

			existingAPIServices: []runtime.Object{
				runtime.Object(newAPIService("build.openshift.io", "v1")),
				runtime.Object(newAPIService("apps.openshift.io", "v1")),
			},
			apiServiceReactor: func(action kubetesting.Action) (handled bool, ret runtime.Object, err error) {
				if action.GetVerb() == "get" && action.(kubetesting.GetAction).GetName() == "v1.build.openshift.io" {
					return true, &apiregistrationv1.APIService{
						ObjectMeta: metav1.ObjectMeta{Name: "v1.build.openshift.io", Annotations: map[string]string{"service.alpha.openshift.io/inject-cabundle": "true"}},
						Spec: apiregistrationv1.APIServiceSpec{
							Group:                "build.openshift.io",
							Version:              "v1",
							Service:              &apiregistrationv1.ServiceReference{Namespace: operatorclient.TargetNamespace, Name: "api"},
							GroupPriorityMinimum: 9900,
							VersionPriority:      15,
						},
						Status: apiregistrationv1.APIServiceStatus{
							Conditions: []apiregistrationv1.APIServiceCondition{
								{Type: apiregistrationv1.Available, Status: apiregistrationv1.ConditionFalse, Message: "TEST MESSAGE"},
							},
						},
					}, nil
				}
				return false, nil, nil
			},
		},
		{
			name:            "MultipleAPIServiceNotAvailable",
			expectedStatus:  operatorv1.ConditionFalse,
			expectedReasons: []string{"Error"},
			expectedMessages: []string{
				"apiservices.apiregistration.k8s.io/v1.apps.openshift.io: not available: TEST MESSAGE",
				"apiservices.apiregistration.k8s.io/v1.build.openshift.io: not available: TEST MESSAGE",
			},

			existingAPIServices: []runtime.Object{
				runtime.Object(newAPIService("build.openshift.io", "v1")),
				runtime.Object(newAPIService("apps.openshift.io", "v1")),
			},
			apiServiceReactor: func(action kubetesting.Action) (handled bool, ret runtime.Object, err error) {
				if action.GetVerb() != "get" {
					return false, nil, nil
				}

				switch action.(kubetesting.GetAction).GetName() {
				case "v1.build.openshift.io":
					fallthrough
				case "v1.apps.openshift.io":
					return true, &apiregistrationv1.APIService{
						ObjectMeta: metav1.ObjectMeta{Name: action.(kubetesting.GetAction).GetName(), Annotations: map[string]string{"service.alpha.openshift.io/inject-cabundle": "true"}},
						Spec: apiregistrationv1.APIServiceSpec{
							Group:                action.GetResource().Group,
							Version:              action.GetResource().Version,
							Service:              &apiregistrationv1.ServiceReference{Namespace: operatorclient.TargetNamespace, Name: "api"},
							GroupPriorityMinimum: 9900,
							VersionPriority:      15,
						},
						Status: apiregistrationv1.APIServiceStatus{
							Conditions: []apiregistrationv1.APIServiceCondition{
								{Type: apiregistrationv1.Available, Status: apiregistrationv1.ConditionFalse, Message: "TEST MESSAGE"},
							},
						},
					}, nil
				default:
					return false, nil, nil
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			kubeClient := fake.NewSimpleClientset()
			kubeAggregatorClient := kubeaggregatorfake.NewSimpleClientset(tc.existingAPIServices...)
			if tc.apiServiceReactor != nil {
				kubeAggregatorClient.PrependReactor("*", "apiservices", tc.apiServiceReactor)
			}

			fakeOperatorClient := operatorv1helpers.NewFakeOperatorClient(&operatorv1.OperatorSpec{ManagementState: operatorv1.Managed}, &operatorv1.OperatorStatus{}, nil)
			operator := &APIServiceController{
				apiServices: []*apiregistrationv1.APIService{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "v1.apps.openshift.io"},
						Spec:       apiregistrationv1.APIServiceSpec{Group: "apps.openshift.io", Version: "v1", Service: &apiregistrationv1.ServiceReference{}},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "v1.build.openshift.io"},
						Spec:       apiregistrationv1.APIServiceSpec{Group: "build.openshift.io", Version: "v1", Service: &apiregistrationv1.ServiceReference{}},
					},
				},
				precondition:            func() (bool, error) { return true, nil },
				kubeClient:              kubeClient,
				eventRecorder:           events.NewInMemoryRecorder(""),
				operatorClient:          fakeOperatorClient,
				apiregistrationv1Client: kubeAggregatorClient.ApiregistrationV1(),
				versionRecorder:         status.NewVersionGetter(),
			}

			_ = operator.sync()

			_, resultStatus, _, err := fakeOperatorClient.GetOperatorState()
			if err != nil {
				t.Fatal(err)
			}
			condition := operatorv1helpers.FindOperatorCondition(resultStatus.Conditions, "APIServicesAvailable")
			if condition == nil {
				t.Fatal("APIServicesAvailable condition not found")
			}
			if condition.Status != tc.expectedStatus {
				t.Error(diff.ObjectGoPrintSideBySide(condition.Status, tc.expectedStatus))
			}
			expectedReasons := strings.Join(tc.expectedReasons, "\n")
			if len(expectedReasons) > 0 && condition.Reason != expectedReasons {
				t.Error(diff.ObjectGoPrintSideBySide(condition.Reason, expectedReasons))
			}
			if len(tc.expectedMessages) > 0 {
				actualMessages := strings.Split(condition.Message, "\n")
				a := make([]string, len(tc.expectedMessages))
				b := make([]string, len(actualMessages))
				copy(a, tc.expectedMessages)
				copy(b, actualMessages)
				sort.Strings(a)
				sort.Strings(b)
				if !equality.Semantic.DeepEqual(a, b) {
					t.Error("\n" + diff.ObjectDiff(a, b))
				}
			}
		})
	}

}

func newAPIService(group, version string) *apiregistrationv1.APIService {
	return &apiregistrationv1.APIService{
		ObjectMeta: metav1.ObjectMeta{Name: version + "." + group, Annotations: map[string]string{"service.alpha.openshift.io/inject-cabundle": "true"}},
		Spec:       apiregistrationv1.APIServiceSpec{Group: group, Version: version, Service: &apiregistrationv1.ServiceReference{Namespace: operatorclient.TargetNamespace, Name: "api"}, GroupPriorityMinimum: 9900, VersionPriority: 15},
		Status:     apiregistrationv1.APIServiceStatus{Conditions: []apiregistrationv1.APIServiceCondition{{Type: apiregistrationv1.Available, Status: apiregistrationv1.ConditionTrue}}},
	}
}
