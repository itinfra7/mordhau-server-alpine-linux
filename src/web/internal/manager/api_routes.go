package manager

import "net/http"

type authenticatedHandler func(http.ResponseWriter, *http.Request, Session)

type managerAPIRoute struct {
	Path        string
	Handler     authenticatedHandler
	FleetRemote bool
}

func (m *Manager) managerAPIRoutes() []managerAPIRoute {
	return []managerAPIRoute{
		{"/api/me", m.meHandler, false},
		{"/api/snapshot", m.snapshotHandler, true},
		{"/api/events", m.eventsHandler, true},
		{"/api/runtime/status", m.runtimeStatusHandler, true},
		{"/api/runtime/target", m.runtimeTargetHandler, true},
		{"/api/runtime/property", m.runtimePropertyHandler, true},
		{"/api/players", m.playersHandler, true},
		{"/api/players/detail", m.playerDetailHandler, true},
		{"/api/players/restriction", m.playerRestrictionHandler, true},
		{"/api/players/action", m.playerActionHandler, true},
		{"/api/players/comments", m.playerCommentHandler, true},
		{"/api/server/action", m.serverActionHandler, true},
		{"/api/server/update-status", m.steamUpdateStatusHandler, true},
		{"/api/server/update-check", m.steamUpdateCheckHandler, true},
		{"/api/updates/automatic", m.automaticUpdateSettingsHandler, true},
		{"/api/server/restart-schedule", m.scheduledServerRestartSettingsHandler, true},
		{"/api/recovery/settings", m.recoverySettingsHandler, true},
		{"/api/recovery/retry", m.recoveryRetryHandler, true},
		{"/api/monitoring", m.monitoringHandler, true},
		{"/api/monitoring/settings", m.monitoringSettingsHandler, true},
		{"/api/monitoring/webhook/test", m.monitoringWebhookTestHandler, true},
		{"/api/monitoring/metrics", m.monitoringMetricsHandler, true},
		{"/api/monitoring/logs", m.monitoringLogsHandler, true},
		{"/api/monitoring/logs/export", m.monitoringLogsExportHandler, true},
		{"/api/maps", m.mapCatalogHandler, true},
		{"/api/maps/change", m.mapChangeHandler, true},
		{"/api/maprotation", m.mapRotationHandler, true},
		{"/api/maprotation/save", m.mapRotationSaveHandler, true},
		{"/api/server/events/history", m.rconHistoryHandler, true},
		{"/api/rcon/history", m.rconHistoryHandler, true},
		{"/api/rcon/message", m.rconMessageHandler, true},
		{"/api/rcon/command", m.rconCommandHandler, true},
		{"/api/language", m.languageHandler, true},
		{"/api/config", m.configHandler, true},
		{"/api/config/mutate", m.configMutationHandler, true},
		{"/api/config/discard", m.configDiscardHandler, true},
		{"/api/mods", m.modsHandler, true},
		{"/api/mods/refresh", m.modRefreshHandler, true},
		{"/api/mods/refresh/settings", m.modRefreshSettingsHandler, true},
		{"/api/mods/plan", m.modPlanHandler, true},
		{"/api/mods/add", m.modAddHandler, true},
		{"/api/mods/enabled", m.modEnabledHandler, true},
		{"/api/mods/remove/plan", m.modRemovePlanHandler, true},
		{"/api/mods/remove", m.modRemoveHandler, true},
		{"/api/modio/settings", m.modIOSettingsHandler, true},
		{"/api/modio/settings/clear", m.modIOSettingsClearHandler, true},
		{"/api/custompaks", m.customPaksHandler, true},
		{"/api/custompaks/upload", m.customPakUploadHandler, true},
		{"/api/custompaks/enabled", m.customPakEnabledHandler, true},
		{"/api/custompaks/delete", m.customPakDeleteHandler, true},
		{"/api/custompaks/delete/cancel", m.customPakDeleteCancelHandler, true},
		{"/api/accounts", m.accountsHandler, true},
		{"/api/accounts/create", m.accountCreateHandler, true},
		{"/api/accounts/edit", m.accountEditHandler, true},
		{"/api/accounts/delete", m.accountDeleteHandler, true},
		{"/api/access", m.accessHandler, true},
		{"/api/access/base", m.accessBaseHandler, true},
		{"/api/access/rule", m.accessRuleHandler, true},
		{"/api/access/rule/delete", m.accessRuleDeleteHandler, true},
		{"/api/services", m.servicesHandler, true},
		{"/api/services/mode", m.serviceModeHandler, true},
		{"/api/services/web-port", m.webPortHandler, true},
		{"/api/services/server-ports", m.serverPortsHandler, true},
		{"/api/services/start-map", m.startMapHandler, true},
		{"/api/manager/update", m.managerUpdateStatusHandler, true},
		{"/api/manager/update/check", m.managerUpdateCheckHandler, true},
		{"/api/manager/update/apply", m.managerUpdateApplyHandler, true},
	}
}

func (m *Manager) fleetRemoteRoute(path string) (authenticatedHandler, bool) {
	for _, route := range m.managerAPIRoutes() {
		if route.Path == path && route.FleetRemote {
			return route.Handler, true
		}
	}
	return nil, false
}
