module digitalis.io/vals-operator

go 1.16

require (
	github.com/go-logr/logr v0.3.0
	github.com/go-sql-driver/mysql v1.5.0
	github.com/gocql/gocql v0.0.0-20211015133455-b225f9b53fa1
	github.com/hashicorp/vault/api v1.3.0
	github.com/hashicorp/vault/api/auth/approle v0.1.1 // indirect
	github.com/hashicorp/vault/api/auth/kubernetes v0.1.0 // indirect
	github.com/hashicorp/vault/api/auth/userpass v0.1.0 // indirect
	github.com/lib/pq v1.2.0
	github.com/mitchellh/mapstructure v1.4.2 // indirect
	github.com/variantdev/vals v0.15.0
	k8s.io/api v0.20.2
	k8s.io/apimachinery v0.20.2
	k8s.io/client-go v0.20.2
	sigs.k8s.io/controller-runtime v0.8.3
)
