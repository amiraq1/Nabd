package presentation

import "nabd/internal/agent"

var permissionReasonArabic = map[agent.PermissionReason]string{
	agent.PermissionReasonToolNoName:         "أداة بلا اسم",
	agent.PermissionReasonUnknownTool:        "أداة غير معروفة",
	agent.PermissionReasonPlanReadOnly:       "وضع الخطة للقراءة فقط",
	agent.PermissionReasonSessionGrant:       "مسموح لهذه الجلسة",
	agent.PermissionReasonPolicyDenied:       "مرفوض وفق سياسة الأذونات",
	agent.PermissionReasonRequired:           "يتطلب موافقة",
	agent.PermissionReasonUnknownOrForbidden: "أداة مجهولة أو محظورة",
	agent.PermissionReasonNoPrompt:           "لا توجد واجهة لطلب الإذن",
}

// PermissionReasonText is the only permission-reason translation boundary.
// A stable reason is authoritative. Legacy events have no reason and retain
// their exact Text. Unknown new reasons fail closed to an empty rendering
// rather than presenting an unreviewed fallback as localized policy text.
func PermissionReasonText(e agent.Event) string {
	if e.Reason == "" {
		return e.Text
	}
	if !e.Reason.Valid() {
		return ""
	}
	return permissionReasonArabic[e.Reason]
}
