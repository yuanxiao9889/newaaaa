package authz

const ResourceRechargeRecord = "recharge_record"

var (
	RechargeRecordRead         = Permission{Resource: ResourceRechargeRecord, Action: ActionRead}
	RechargeRecordInvoiceWrite = Permission{Resource: ResourceRechargeRecord, Action: ActionWrite}
)

func init() {
	RegisterResource(ResourceDefinition{
		Resource: ResourceRechargeRecord,
		LabelKey: "Recharge records",
		Actions: []ActionDefinition{
			{
				Action:         ActionRead,
				LabelKey:       "View recharge records",
				DescriptionKey: "View recharge records and their invoice status across users.",
				DefaultRoles:   []string{BuiltInRoleAdmin},
			},
			{
				Action:         ActionWrite,
				LabelKey:       "Maintain invoice markings",
				DescriptionKey: "Mark, correct, or revoke invoice status for recharge records. Dashboard login session required.",
				DefaultRoles:   []string{BuiltInRoleAdmin},
			},
		},
	})
}
