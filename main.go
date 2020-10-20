package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/labels"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/pingcap/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	log "github.com/sirupsen/logrus"
)

type config []struct {
	Targets []string `yaml:"targets"`
}

func main() {
	err := validateEnvironmentVariables()
	if err != nil {
		log.WithError(err).Error("Environment variable validation failed")
		err = sendMattermostErrorNotification(err, "Environment variable validation failed")
		if err != nil {
			log.WithError(err).Error("Failed to send Mattermost error notification")
		}
		return
	}

	err = thanosStoreDiscovery()
	if err != nil {
		log.WithError(err).Error("Failed to run Thanos store discovery")
		err = sendMattermostErrorNotification(err, "The Thanos store discovery failed")
		if err != nil {
			log.WithError(err).Error("Failed to send Mattermost error notification")
		}
		return
	}
}

// validateEnvironmentVariables is used to validate the environment variables needed by Thanos store discovery.
func validateEnvironmentVariables() error {
	privateHostedZoneID := os.Getenv("PRIVATE_HOSTED_ZONE_ID")
	if len(privateHostedZoneID) == 0 {
		return errors.Errorf("PRIVATE_HOSTED_ZONE_ID environment variable is not set")
	}

	thanosNamespace := os.Getenv("THANOS_NAMESPACE")
	if len(thanosNamespace) == 0 {
		return errors.Errorf("THANOS_NAMESPACE environment variable is not set")
	}

	thanosDeploymentName := os.Getenv("THANOS_DEPLOYMENT_NAME")
	if len(thanosDeploymentName) == 0 {
		return errors.Errorf("THANOS_DEPLOYMENT_NAME environment variable is not set.")
	}

	thanosConfigMapName := os.Getenv("THANOS_CONFIGMAP_NAME")
	if len(thanosConfigMapName) == 0 {
		return errors.Errorf("THANOS_CONFIGMAP_NAME environment variable is not set.")
	}

	mattermostAlertsHook := os.Getenv("MATTERMOST_ALERTS_HOOK")
	if len(mattermostAlertsHook) == 0 {
		return errors.Errorf("MATTERMOST_ALERTS_HOOK environment variable is not set.")
	}
	return nil
}

// thanosStoreDiscovery is used to keep Thanos up to date with deployed Thanos Query targets.
func thanosStoreDiscovery() error {
	privateHostedZoneID := os.Getenv("PRIVATE_HOSTED_ZONE_ID")
	thanosNamespace := os.Getenv("THANOS_NAMESPACE")
	thanosDeploymentName := os.Getenv("THANOS_DEPLOYMENT_NAME")
	thanosConfigMapName := os.Getenv("THANOS_CONFIGMAP_NAME")

	log.Infof("Getting Route53 records for hostedzone %s", privateHostedZoneID)
	records, err := listAllRecordSets(privateHostedZoneID)
	if err != nil {
		return errors.Wrap(err, "Unable to get the existing Thanos Route53 records")
	}

	thanosRecords := getThanosTargets(records)
	if len(thanosRecords) < 1 {
		log.Info("No Thanos records to register, canceling run")
		return nil
	}
	route53Targets := addPortToTargets(thanosRecords)

	// This part can be used for local testing
	// kubeconfig := filepath.Join(
	// 	os.Getenv("HOME"), ".kube", "config",
	// )

	// config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	// if err != nil {
	// 	return err
	// }

	config, err := rest.InClusterConfig()
	if err != nil {
		return errors.Wrap(err, "Unable to set k8s access config")
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return errors.Wrap(err, "Unable to create k8s clientset")
	}

	configMapTargets, err := getConfigMapTargets(thanosConfigMapName, thanosNamespace, clientset)
	if err != nil && !errors.IsNotFound(err) {
		return errors.Wrap(err, "Unable to get existing configmap targets")
	}

	configMapUpToDate := checkIfConfigMapUpToDate(configMapTargets, route53Targets)

	if len(configMapTargets) < 1 || !configMapUpToDate {
		log.Info("The Thanos configmap targets are not up to date with existing Route53 Thanos targets")
		err = createOrUpdateTargetConfigMap(route53Targets, thanosConfigMapName, thanosNamespace, clientset)
		if err != nil {
			return errors.Wrap(err, "Unable to create or update the Thanos Query configmap")
		}

		err = rotateQueryPods(thanosNamespace, thanosDeploymentName, clientset)
		if err != nil {
			return errors.Wrap(err, "Unable to rotate Thanos Query pods")
		}

		log.Info("Successfully rotated all Thanos Query pods")
	} else {
		log.Info("The Thanos configmap targets are up to date with existing Route53 Thanos targets")
	}
	return nil
}

// checkIfConfigMapUpToDate is used to check if the current configmap targets are up to date with the deploye Route53 targets.
func checkIfConfigMapUpToDate(configMapTargets, route53Targets []string) bool {
	if len(route53Targets) != len(configMapTargets) {
		return false
	}

	for i, v := range route53Targets {
		if v != configMapTargets[i] {
			return false
		}
	}
	return true
}

// getConfigMapTargets is used to get the existing Thanos Query targets defined in the configmap
func getConfigMapTargets(thanosConfigMapName, thanosNamespace string, clientset *kubernetes.Clientset) ([]string, error) {
	ctx := context.TODO()
	configMap, err := clientset.CoreV1().ConfigMaps(thanosNamespace).Get(ctx, thanosConfigMapName, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return nil, errors.Wrap(err, "Unable to get the Thanos Query configmap")
	}
	data := (configMap.Data["servicediscovery.yml"])
	configStructure := config{}
	log.Info("Decoding configmap data into structure")
	configDecoded, err := decodeConfig(data, configStructure)
	if err != nil {
		return nil, errors.Wrap(err, "Unable to decode into struct")
	}
	log.Info("Successfully decoded configmap data into structure")
	if len(configDecoded) > 0 {
		return configDecoded[0].Targets, nil
	}
	return []string{}, nil
}

// decodeConfig is used to decode the configmap data into a usable structure
func decodeConfig(data string, C config) (config, error) {
	dataByte := []byte(data)
	err := yaml.Unmarshal(dataByte, &C)
	if err != nil {
		return C, err
	}
	return C, nil
}

// addPortToTargets add the port number in each Thanos target to prepare them for configmap deployment
func addPortToTargets(thanosRecords []string) []string {
	var thanosTargets []string
	for _, record := range thanosRecords {
		thanosTargets = append(thanosTargets, fmt.Sprintf("%s:10901", record))
	}
	return thanosTargets
}

// listAllRecordSets is used to get the existing Route53 Records
func listAllRecordSets(hostedZoneID string) ([]*route53.ResourceRecordSet, error) {
	var err error

	sess, err := session.NewSession()
	if err != nil {
		return nil, err
	}

	// Create Route53 service client
	svc := route53.New(sess)

	req := route53.ListResourceRecordSetsInput{
		HostedZoneId:    aws.String(hostedZoneID),
		StartRecordName: aws.String("c"),
		StartRecordType: aws.String("CNAME"),
	}

	var rrsets []*route53.ResourceRecordSet

	for {
		var resp *route53.ListResourceRecordSetsOutput
		resp, err = svc.ListResourceRecordSets(&req)
		if err != nil {
			return nil, err
		}
		rrsets = append(rrsets, resp.ResourceRecordSets...)
		if *resp.IsTruncated {
			req.StartRecordName = resp.NextRecordName
			req.StartRecordType = resp.NextRecordType
			req.StartRecordIdentifier = resp.NextRecordIdentifier
		} else {
			break
		}
	}

	return rrsets, nil
}

// getThanosTargets is used to get only Thanos related Route53 records.
func getThanosTargets(recordSets []*route53.ResourceRecordSet) []string {
	thanosTargets := []string{}
	for _, record := range recordSets {
		if strings.Contains(*record.Name, "-grpc.") {
			thanosTargets = append(thanosTargets, *record.Name)
		}
	}
	log.Info("Returning matching Thanos Route53 records")
	return thanosTargets
}

// createOrUpdateTargetConfigMap is used to create or update the Thanos target configmap
func createOrUpdateTargetConfigMap(targets []string, thanosConfigMapName, thanosNamespace string, clientset *kubernetes.Clientset) error {

	serviceDiscovery := fmt.Sprintf("- targets:\n  - %s", strings.Join(targets, "\n  - "))

	configMapData := make(map[string]string, 0)
	configMapData["servicediscovery.yml"] = serviceDiscovery
	configMap := corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      thanosConfigMapName,
			Namespace: thanosNamespace,
		},
		Data: configMapData,
	}
	ctx := context.TODO()
	_, err := clientset.CoreV1().ConfigMaps(thanosNamespace).Get(ctx, thanosConfigMapName, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}

	if err != nil && errors.IsNotFound(err) {
		log.Infof("Configmap %s does not exist. Creating...", thanosConfigMapName)
		_, err = clientset.CoreV1().ConfigMaps(thanosNamespace).Create(ctx, &configMap, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	} else {
		log.Infof("Configmap %s already exists. Updating...", thanosConfigMapName)
		_, err := clientset.CoreV1().ConfigMaps(thanosNamespace).Update(ctx, &configMap, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}
	return nil
}

// rotateQueryPods is used to rotate all Thanos Query pods
func rotateQueryPods(thanosNamespace, thanosDeploymentName string, clientset *kubernetes.Clientset) error {
	queryPods, err := getPodsFromDeployment(thanosNamespace, thanosDeploymentName, clientset)
	if err != nil && k8serrors.IsNotFound(err) {
		log.Infof("No Thanos Query deployment found. Assuming that Thanos is not deployed, moving on")
		return nil
	} else if err != nil && !k8serrors.IsNotFound(err) {
		return err
	}

	wait := 180

	for _, pod := range queryPods.Items {
		log.Infof("Deleting pod %q", pod.GetName())
		err = deletePod(thanosNamespace, pod.GetName(), clientset)
		if err != nil {
			return err
		}

		queryPods, err = getPodsFromDeployment(thanosNamespace, thanosDeploymentName, clientset)
		if err != nil {
			return err
		}
		log.Infof("Waiting up to %d seconds for all %q pods to start...", wait, thanosDeploymentName)
		for _, newPod := range queryPods.Items {
			if newPod.GetName() != pod.GetName() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Duration(wait)*time.Second)
				defer cancel()
				_, err := waitForPodRunning(ctx, thanosNamespace, newPod.GetName(), clientset)
				if err != nil {
					return err
				}
			}

		}
	}
	return nil
}

// deletePod is used to delete an existing pod
func deletePod(namespace, podName string, clientset *kubernetes.Clientset) error {
	ctx := context.TODO()
	err := clientset.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{})
	if err != nil {
		return err
	}
	return nil
}

// getPodsFromDeployment is used to get the pods of a deployment
func getPodsFromDeployment(namespace, deploymentName string, clientset *kubernetes.Clientset) (*corev1.PodList, error) {
	ctx := context.TODO()
	deployment, err := clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	set := labels.Set(deployment.GetLabels())
	listOptions := metav1.ListOptions{LabelSelector: set.AsSelector().String()}

	return clientset.CoreV1().Pods(namespace).List(ctx, listOptions)
}

// waitForPodRunning is used to ensure that a pod is running and status condition is ready
func waitForPodRunning(ctx context.Context, namespace, podName string, clientset *kubernetes.Clientset) (*corev1.Pod, error) {
	for {
		pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		podReady := true
		if err == nil {
			for _, condition := range pod.Status.Conditions {
				if condition.Status == corev1.ConditionFalse {
					podReady = false
				}
			}
			if podReady == true {
				return pod, nil
			}
		}
		if err != nil && k8serrors.IsNotFound(err) {
			log.Infof("Pod %s not found in %s namespace, maybe was part of the old replicaset, since we updated the deployment/statefullsets, moving on", podName, namespace)
			return &corev1.Pod{}, nil
		}

		select {
		case <-ctx.Done():
			return nil, errors.Wrap(ctx.Err(), "timed out waiting for pod to become ready")
		case <-time.After(5 * time.Second):
		}
	}
}
