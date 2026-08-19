package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DNSZoneSpec defines a tenant DNS zone managed via ExternalDNS (§5.6).
type DNSZoneSpec struct {
	TenantReference `json:",inline"`

	// ZoneName is the DNS zone (e.g. "team-a.apps.example.com").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ZoneName string `json:"zoneName"`
}

// DNSZoneStatus exposes observed state for the Resources Inventory.
type DNSZoneStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +optional
	ZoneName string `json:"zoneName,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Tenant",type=string,JSONPath=`.spec.tenantID`
// +kubebuilder:printcolumn:name="Zone",type=string,JSONPath=`.status.zoneName`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// DNSZone is a platform-scoped Catalog Item delegating a DNS zone to a
// tenant; records are rendered as ExternalDNS DNSEndpoints (§5.6).
type DNSZone struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DNSZoneSpec   `json:"spec,omitempty"`
	Status DNSZoneStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DNSZoneList contains a list of DNSZone.
type DNSZoneList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DNSZone `json:"items"`
}

// DNSRecordSpec defines DNS records inside a tenant zone.
type DNSRecordSpec struct {
	TenantReference `json:",inline"`

	// ZoneRef references the DNSZone this record belongs to.
	// +kubebuilder:validation:Required
	ZoneRef string `json:"zoneRef"`

	// Endpoints are the ExternalDNS-style endpoint entries.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Endpoints []DNSRecordEndpoint `json:"endpoints"`
}

// DNSRecordEndpoint is a single DNS endpoint entry.
type DNSRecordEndpoint struct {
	// DNSName is the record name (e.g. "app.team-a.apps.example.com").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	DNSName string `json:"dnsName"`

	// RecordType is the DNS record type (A, CNAME, TXT, ...).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=A;AAAA;CNAME;TXT;MX;NS;SRV
	RecordType string `json:"recordType"`

	// Targets are the record values.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Targets []string `json:"targets"`

	// TTL in seconds.
	// +optional
	// +kubebuilder:default=300
	TTL int64 `json:"ttl,omitempty"`
}

// DNSRecordStatus exposes observed state for the Resources Inventory.
type DNSRecordStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Tenant",type=string,JSONPath=`.spec.tenantID`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// DNSRecord is a platform-scoped Catalog Item rendering ExternalDNS
// DNSEndpoints for tenant records (§5.6).
type DNSRecord struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DNSRecordSpec   `json:"spec,omitempty"`
	Status DNSRecordStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DNSRecordList contains a list of DNSRecord.
type DNSRecordList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DNSRecord `json:"items"`
}
