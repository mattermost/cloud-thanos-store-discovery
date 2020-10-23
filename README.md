Mattermost Cloud Thanos Store Discovery
====================================================

Mattermost Thanos store discovery tool is a microservice designed to work in a multi-cluster environment, with the purpose to automatically register new Thanos query endpoints in a central Thanos deployment.

## How it works?

This microservice needs to run in the same cluster and namespace that the central Thanos deployment is running. It can be deployed as a cronjob that runs every x amount of minutes and keeps the central Thanos Querier up to date with all the target endpoints. More details on how it works can be seen in the following diagram.

<span style="display:block;text-align:center">![store-discovery](/img/store-discovery.png)</span>

As it can be seen the tool looks for record names that end with word `-grpc` and then it gets the configmap (if it exists) that is used by the central Thanos deployment. It checks if the records in Route53 match the endpoints defined in the confimap and if not it updates the configmap. The last step includes the rotation of the existing Thanos query pods one by one to apply the new configmap. The tools works better when more than one Thanos query pods are used, because this way no downtime can be achieved during the pod rotation.

## In which setup it can be used?

This tool was designed to manage Thanos query endpoint discovery in a multi-cluster environment. In our case we are using a central cluster that gets metrics from multiple other clusters. These clusters are not created at the same time and we needed an automatic way to tell our central cluster about their existence.

<span style="display:block;text-align:center">![thanos-arc](/img/thanos-arch.png)</span>

The cloud Thanos store discovery tool runs in the central cluster (CNC) and in the same namespace that Thanos Querier runs. The Thanos Querier always looks in the specified configmap (which is managed by the tool) for query store endpoints.

## How to run?

The `mattermost/cloud-thanos-store-discovery` docker image can be used to run the microservice but some additional environment variables need to be exported to make it work. In more detail:

```
PRIVATE_HOSTED_ZONE_ID -> The hosted zone id to look for Thanos query records.

THANOS_NAMESPACE -> The namespace that Thanos is deployed.

THANOS_DEPLOYMENT_NAME -> The name of the Thanos query deployment.

THANOS_CONFIGMAP_NAME -> The name of the configmap that is/will be used for query endpoint targets.

MATTERMOST_ALERTS_HOOK -> A hook for error notifications.
```

This tool will need access to get/create configmaps as well as get/list deployments and get/list/delete pods. Therefore, the relevant role, serviceaccount and rolebinding should be created.

## Local testing?

For local testing all the environent variables specified above should be set. In addition, the **DEVELOPER_MODE** environment variable should be set to **true** to be able to use local k8s configuration for cluster authentication.
