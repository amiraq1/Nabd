package presentation

import (
	"nabd/internal/event"
)

var permissionReasonArabic = map[event.PermissionReason]string{
	event.PermissionReasonToolNoName:         "أداة بلا اسم",
	event.PermissionReasonUnknownTool:        "أداة غير معروفة",
	event.PermissionReasonPlanReadOnly:       "وضع الخطة للقراءة فقط",
	event.PermissionReasonSessionGrant:       "مسموح لهذه الجلسة",
	event.PermissionReasonPolicyDenied:       "مرفوض وفق سياسة الأذونات",
	event.PermissionReasonRequired:           "يتطلب موافقة",
	event.PermissionReasonUnknownOrForbidden: "أداة مجهولة أو محظورة",
	event.PermissionReasonNoPrompt:           "لا توجد واجهة لطلب الإذن",
}

// PermissionReasonText is the only permission-reason translation boundary.
// A stable reason is authoritative. Legacy events have no reason and retain
// their exact Text. Unknown new reasons fail closed to an empty rendering
// rather than presenting an unreviewed fallback as localized policy text.
func PermissionReasonText(e event.Event) string {
	if e.Reason == "" {
		return e.Text
	}
	if !e.Reason.Valid() {
		return ""
	}
	return permissionReasonArabic[e.Reason]
}
