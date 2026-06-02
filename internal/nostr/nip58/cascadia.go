package nip58

// Cascadia permission badge d-tag values.
const (
	BadgeCanDeploy           = "cascadia:can-deploy"
	BadgeCanDeployProduction = "cascadia:can-deploy:production"
	BadgeCanApprove          = "cascadia:can-approve"
	BadgeCanMerge            = "cascadia:can-merge"
	BadgeCanSign             = "cascadia:can-sign"
	BadgeCanOperate          = "cascadia:can-operate"
	BadgeCanAdmin            = "cascadia:can-admin"
)

// Cascadia trust level badge d-tag values.
const (
	BadgeTrustVerified  = "cascadia:trust:verified"
	BadgeTrustAudited   = "cascadia:trust:audited"
	BadgeTrustCompliant = "cascadia:trust:compliant"
)

// Cascadia certification badge d-tag values.
const (
	BadgeCertSecurityReview = "cascadia:cert:security-review"
	BadgeCertSBOMAttested   = "cascadia:cert:sbom-attested"
	BadgeCertProvenance     = "cascadia:cert:provenance"
)

// CascadiaBadges lists every Cascadia badge d-tag known to this package.
var CascadiaBadges = []string{
	BadgeCanDeploy,
	BadgeCanDeployProduction,
	BadgeCanApprove,
	BadgeCanMerge,
	BadgeCanSign,
	BadgeCanOperate,
	BadgeCanAdmin,
	BadgeTrustVerified,
	BadgeTrustAudited,
	BadgeTrustCompliant,
	BadgeCertSecurityReview,
	BadgeCertSBOMAttested,
	BadgeCertProvenance,
}

// IsCascadiaBadge reports whether dtag is one of the known Cascadia badge d-tags.
func IsCascadiaBadge(dtag string) bool {
	for _, badge := range CascadiaBadges {
		if badge == dtag {
			return true
		}
	}
	return false
}
