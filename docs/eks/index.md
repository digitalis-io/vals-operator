# EKS Integration

## EKS Configuration

To use this feature you must have an EKS cluster configured with OIDC identity provider.
This is out of scope for this documentation but please check out AWS website for more
information:

https://aws.amazon.com/blogs/containers/introducing-oidc-identity-provider-authentication-amazon-eks/
https://docs.aws.amazon.com/eks/latest/userguide/enable-iam-roles-for-service-accounts.html
https://docs.aws.amazon.com/eks/latest/userguide/create-service-account-iam-policy-and-role.html
https://docs.aws.amazon.com/eks/latest/userguide/specify-service-account-role.html

## Trust

```json
{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Effect": "Allow",
            "Principal": {
                "Federated": "arn:aws:iam::MY-ACCOUNT:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/MY-OIDC"
            },
            "Action": "sts:AssumeRoleWithWebIdentity",
            "Condition": {
                "StringEquals": {
                    "oidc.eks.us-east-1.amazonaws.com/id/MY-OIDC:aud": "sts.amazonaws.com",
                    "oidc.eks.us-east-1.amazonaws.com/id/MY-OIDC:sub": "system:serviceaccount:vals:vals"
                }
            }
        }
    ]
}
```

## Policy

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "secretsmanager:GetResourcePolicy",
        "secretsmanager:GetSecretValue",
        "secretsmanager:DescribeSecret",
        "secretsmanager:ListSecretVersionIds"
      ],
      "Resource": [
        "*"
      ]
    },
    {
      "Effect": "Allow",
      "Action": "secretsmanager:ListSecrets",
      "Resource": "*"
    }
  ]
}
```

## Service Account (manual setup)

If you're not using the helm chart, you will need to create a service account
like the example below:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::MY-ACCOUNT:role/vals-read-secrets
  name: vals-operator
  namespace: vals-operator
```

## Helm Chart Installation

Once you have all the required IAM roles and configs, when installing the helm chart
you will need to pass on the annotation as a parameter:

```sh
helm upgrade --install vals-operator --create-namespace -n vals-operator \
  --set serviceAccount.annotations.eks\\.amazonaws\\.com/role-arn=arn:aws:iam::MY-ACCOUNT:role/vals-read-secrets
```
