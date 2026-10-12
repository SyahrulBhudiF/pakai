import QtQuick
import Quickshell
import qs.Common
import qs.Services
import qs.Widgets
import qs.Modules.Plugins

PluginComponent {
    id: root

    layerNamespacePlugin: "pakai"

    property string daemonUrl: pluginData.daemonUrl || "http://127.0.0.1:7731"
    property int refreshInterval: Math.max(15, Number(pluginData.refreshInterval) || 60) * 1000
    property bool showLabel: pluginData.showLabel ?? false
    property var providers: []
    property bool loading: false
    property string errorMessage: ""
    property date lastRefresh: new Date(0)
    property real worstPct: -1
    property int providerErrorCount: 0
    property string pinnedProvider: ""
    property var providerSettings: ({})
    property var providerSources: ({})
    property bool showingSettings: false
    property var providerOptions: [
        { providerId: "claude", label: "Claude", source: "Claude OAuth" },
        { providerId: "openai", label: "Codex", defaultSource: "pi", sources: [{ value: "pi", label: "Pi" }, { value: "codex", label: "Codex CLI" }] },
        { providerId: "opencode-go", label: "OpenCode Go", defaultSource: "pi", sources: [{ value: "pi", label: "Pi" }, { value: "opencode", label: "OpenCode" }] },
        { providerId: "opencode", label: "OpenCode local", source: "Local SQLite usage" },
        { providerId: "pi", label: "Pi sessions", source: "Local Pi session logs" },
        { providerId: "inferhub", label: "InferHub", source: "InferHub API key" }
    ]
    property real settledPopoutHeight: 170

    readonly property color statusColor: errorMessage !== "" ? Theme.error
        : worstPct < 0 ? Theme.surfaceVariantText
        : worstPct >= 80 ? Theme.error
        : worstPct >= 50 ? Theme.warning
        : Theme.success
    readonly property string compactText: errorMessage !== "" ? "ERR"
        : worstPct >= 0 ? Math.round(worstPct) + "%"
        : providers.length > 0 ? "OK" : "--"

    Timer {
        interval: root.refreshInterval
        running: true
        repeat: true
        onTriggered: root.fetchStatus()
    }

    Connections {
        target: root.pluginService
        function onPluginDataChanged(changedPluginId) {
            if (changedPluginId === root.pluginId)
                Qt.callLater(root.fetchStatus)
        }
    }

    Component.onCompleted: Qt.callLater(function() { fetchStatus(); fetchConfig() })
    onDaemonUrlChanged: Qt.callLater(fetchStatus)
    onShowingSettingsChanged: settledPopoutHeight = desiredPopoutHeight()

    function request(method, path, timeout, done) {
        var xhr = new XMLHttpRequest()
        var completed = false
        function finish(status, body) {
            if (completed) return
            completed = true
            done(status, body)
        }
        xhr.open(method, root.daemonUrl.replace(/\/$/, "") + path)
        xhr.timeout = timeout
        xhr.onreadystatechange = function() {
            if (xhr.readyState === XMLHttpRequest.DONE)
                finish(xhr.status, xhr.responseText)
        }
        xhr.onerror = function() { finish(0, "") }
        xhr.ontimeout = function() { finish(0, "") }
        xhr.send()
    }

    function fetchStatus() {
        if (loading)
            return
        loading = true
        request("GET", "/status", 10000, function(status, body) {
            root.loading = false
            if (status !== 200) {
                root.providers = []
                root.errorMessage = "PakAI daemon is unreachable"
                root.worstPct = -1
                return
            }
            try {
                var data = JSON.parse(body)
                root.providers = Array.isArray(data) ? data : []
                root.errorMessage = ""
                root.updateLastRefresh()
                root.computeWorst()
                root.settledPopoutHeight = root.desiredPopoutHeight()
            } catch (error) {
                root.providers = []
                root.errorMessage = "Invalid daemon response"
                root.worstPct = -1
            }
        })
    }

    function refreshNow() {
        if (loading)
            return
        loading = true
        request("POST", "/api/refresh", 35000, function(status) {
            root.loading = false
            if (status !== 200) {
                root.errorMessage = "Refresh failed"
                return
            }
            root.fetchStatus()
        })
    }

    function percentage(used, limit) {
        return limit > 0 ? used / limit * 100 : -1
    }

    function providerWorst(provider) {
        if (!provider || provider.status === "error")
            return -1
        var result = percentage(Number(provider.used) || 0, Number(provider.limit) || 0)
        var windows = provider.windows || []
        for (var i = 0; i < windows.length; i++)
            result = Math.max(result, percentage(Number(windows[i].used) || 0, Number(windows[i].limit) || 0))
        return result
    }

    function computeWorst() {
        var result = -1
        var errors = 0
        for (var i = 0; i < providers.length; i++) {
            if (providers[i].status === "error") errors++
            if (pinnedProvider !== "" && providers[i].provider === pinnedProvider)
                result = providerWorst(providers[i])
        }
        providerErrorCount = errors
        worstPct = result
    }

    function updateLastRefresh() {
        var newest = 0
        for (var i = 0; i < providers.length; i++) {
            var timestamp = new Date(providers[i].refreshed_at || 0).getTime()
            if (!isNaN(timestamp)) newest = Math.max(newest, timestamp)
        }
        lastRefresh = new Date(newest)
    }

    function usageColor(pct) {
        if (pct < 0) return Theme.surfaceVariantText
        if (pct >= 80) return Theme.error
        if (pct >= 50) return Theme.warning
        return Theme.success
    }

    function formatValue(value, unit) {
        var n = Number(value) || 0
        if (unit === "percent") return Math.round(n) + "%"
        if (unit === "usd") return "$" + n.toFixed(2)
        if (unit === "tokens") {
            if (n >= 1000000) return (n / 1000000).toFixed(1) + "M"
            if (n >= 1000) return (n / 1000).toFixed(1) + "K"
        }
        return n % 1 === 0 ? n.toFixed(0) : n.toFixed(1)
    }

    function windowLabel(window) {
        var key = window.key || window.label || "usage"
        if (key === "monthly") return "Monthly"
        if (key === "weekly") return "Weekly"
        if (key === "5h") return "5 hours"
        return key
    }

    function resetText(value) {
        if (!value) return ""
        var reset = new Date(value)
        if (isNaN(reset.getTime()) || reset.getFullYear() <= 1) return ""
        var seconds = Math.floor((reset.getTime() - Date.now()) / 1000)
        if (seconds <= 0) return "resets now"
        var days = Math.floor(seconds / 86400)
        var hours = Math.floor(seconds % 86400 / 3600)
        var minutes = Math.floor(seconds % 3600 / 60)
        if (days > 0) return "resets in " + days + "d " + hours + "h"
        if (hours > 0) return "resets in " + hours + "h " + minutes + "m"
        return "resets in " + minutes + "m"
    }

    function desiredPopoutHeight() {
        if (showingSettings) return 500
        var height = 136
        for (var i = 0; i < providers.length; i++) {
            var provider = providers[i]
            height += provider.status === "error" ? 50 : 42 + (provider.windows || []).length * 58 + (provider.warning ? 22 : 0)
        }
        return Math.min(520, Math.max(170, height))
    }

    function providerName(provider) {
        if (provider.label && provider.label !== provider.provider) return provider.label
        if (provider.provider === "openai") return "Codex"
        if (provider.provider === "opencode/opencode") return "OpenCode Zen"
        if (provider.provider === "pi/opencode") return "OpenCode Zen · Pi"
        if (provider.provider === "pi/kosyayuk") return "Kosyayuk · Pi"
        if (provider.provider === "opencode-go") return "OpenCode Go"
        if (provider.provider === "inferhub") return "InferHub"
        return provider.provider.charAt(0).toUpperCase() + provider.provider.slice(1)
    }

    function unavailableText(provider) {
        var message = (provider.error || "").toLowerCase()
        if (message.indexOf("not found") >= 0 || message.indexOf("not set") >= 0 || message.indexOf("no claude usage") >= 0)
            return "Not configured"
        return "Unavailable"
    }

    function fetchConfig() {
        request("GET", "/api/config", 10000, function(status, body) {
            if (status !== 200) return
            try {
                var data = JSON.parse(body)
                root.pinnedProvider = data.widget ? (data.widget.pinned || "") : ""
                root.providerSettings = data.providers || ({})
                root.providerSources = data.sources || ({})
                root.computeWorst()
            } catch (error) {}
        })
    }

    function hideProvider(providerId) {
        request("GET", "/api/config?hide=" + encodeURIComponent(providerId), 10000, function(status) {
            if (status === 200) root.fetchStatus()
        })
    }

    function setProviderEnabled(providerId, enabled) {
        var action = enabled ? "enable" : "disable"
        request("GET", "/api/config?" + action + "=" + encodeURIComponent(providerId), 10000, function(status) {
            if (status === 200) {
                root.fetchConfig()
                root.fetchStatus()
            }
        })
    }

    function providerSource(option) {
        return root.providerSources[option.providerId] || option.defaultSource || ""
    }

    function setProviderSource(providerId, source) {
        request("GET", "/api/config?source=" + encodeURIComponent(providerId) + "&value=" + encodeURIComponent(source), 10000, function(status) {
            if (status === 200) {
                root.fetchConfig()
                root.fetchStatus()
            }
        })
    }

    function setPinnedProvider(providerId) {
        var path = providerId === "" ? "/api/config?unpin" : "/api/config?pin=" + encodeURIComponent(providerId)
        request("GET", path, 10000, function(status) {
            if (status === 200) {
                root.fetchConfig()
                root.fetchStatus()
            }
        })
    }

    function refreshedText() {
        if (lastRefresh.getTime() === 0) return "Not refreshed"
        var seconds = Math.floor((Date.now() - lastRefresh.getTime()) / 1000)
        if (seconds < 5) return "Updated now"
        if (seconds < 60) return "Updated " + seconds + "s ago"
        return "Updated " + Math.floor(seconds / 60) + "m ago"
    }

    horizontalBarPill: Component {
        Row {
            spacing: Theme.spacingXS

            DankIcon {
                name: "data_usage"
                size: root.iconSize
                color: root.statusColor
                anchors.verticalCenter: parent.verticalCenter
            }

            StyledText {
                visible: root.showLabel
                text: "PakAI"
                font.pixelSize: Theme.fontSizeSmall
                color: Theme.surfaceText
                anchors.verticalCenter: parent.verticalCenter
            }

            StyledText {
                text: root.loading ? "…" : root.compactText
                font.pixelSize: Theme.fontSizeSmall
                font.weight: Font.Medium
                color: root.statusColor
                anchors.verticalCenter: parent.verticalCenter
            }
        }
    }

    verticalBarPill: Component {
        Column {
            spacing: 2

            DankIcon {
                name: "data_usage"
                size: root.iconSize
                color: root.statusColor
                anchors.horizontalCenter: parent.horizontalCenter
            }

            StyledText {
                text: root.loading ? "…" : root.compactText
                font.pixelSize: Theme.fontSizeSmall - 2
                font.weight: Font.Medium
                color: root.statusColor
                anchors.horizontalCenter: parent.horizontalCenter
            }
        }
    }

    popoutContent: Component {
        PopoutComponent {
            id: popout
            headerText: "PakAI"
            detailsText: root.errorMessage !== "" ? root.errorMessage
                : root.providerErrorCount > 0 ? root.refreshedText() + " · " + root.providerErrorCount + " unavailable"
                : root.refreshedText()
            showCloseButton: true

            DankFlickable {
                visible: !root.showingSettings
                width: parent.width
                implicitHeight: root.popoutHeight - popout.headerHeight - popout.detailsHeight - Theme.spacingS * 2
                contentWidth: width
                contentHeight: content.implicitHeight
                clip: true

                Column {
                    id: content
                    width: parent.width
                    spacing: Theme.spacingM

                    StyledRect {
                        visible: root.errorMessage !== ""
                        width: parent.width
                        height: errorColumn.implicitHeight + Theme.spacingM * 2
                        radius: Theme.cornerRadius
                        color: Theme.errorHover

                        Column {
                            id: errorColumn
                            anchors.fill: parent
                            anchors.margins: Theme.spacingM
                            spacing: Theme.spacingXS

                            StyledText {
                                text: root.errorMessage
                                font.pixelSize: Theme.fontSizeMedium
                                font.weight: Font.Medium
                                color: Theme.error
                            }

                            StyledText {
                                text: "Run: pakai daemon start"
                                font.pixelSize: Theme.fontSizeSmall
                                color: Theme.surfaceText
                            }
                        }
                    }

                    StyledText {
                        visible: root.errorMessage === "" && root.providers.length === 0 && !root.loading
                        text: "No provider data"
                        font.pixelSize: Theme.fontSizeMedium
                        color: Theme.surfaceVariantText
                        anchors.horizontalCenter: parent.horizontalCenter
                    }

                    Repeater {
                        model: root.providers

                        delegate: Item {
                            required property var modelData
                            width: content.width
                            height: providerColumn.implicitHeight + Theme.spacingS * 2

                            Column {
                                id: providerColumn
                                anchors.fill: parent
                                anchors.margins: Theme.spacingS
                                spacing: Theme.spacingS

                                Row {
                                    width: parent.width
                                    spacing: Theme.spacingS

                                    Rectangle {
                                        width: 7
                                        height: 7
                                        radius: 4
                                        color: modelData.status === "error" ? Theme.outlineVariant : root.usageColor(root.providerWorst(modelData))
                                        anchors.verticalCenter: parent.verticalCenter
                                    }

                                    StyledText {
                                        width: parent.width - providerValue.width - 7 - parent.spacing * 2
                                        text: root.providerName(modelData)
                                        font.pixelSize: Theme.fontSizeMedium
                                        font.weight: Font.Medium
                                        color: Theme.surfaceText
                                        elide: Text.ElideRight
                                        anchors.verticalCenter: parent.verticalCenter
                                    }

                                    StyledText {
                                        id: providerValue
                                        text: modelData.status === "error" ? root.unavailableText(modelData)
                                            : root.providerWorst(modelData) >= 0 ? Math.round(root.providerWorst(modelData)) + "%"
                                            : root.formatValue(modelData.used, modelData.unit)
                                        font.pixelSize: Theme.fontSizeSmall
                                        font.weight: Font.Medium
                                        color: modelData.status === "error" ? Theme.surfaceVariantText : root.usageColor(root.providerWorst(modelData))
                                        anchors.verticalCenter: parent.verticalCenter
                                    }
                                }

                                Row {
                                    visible: modelData.status === "error"
                                    width: parent.width
                                    spacing: Theme.spacingS

                                    StyledText {
                                        width: parent.width - hideText.width - parent.spacing
                                        text: modelData.error || "Provider unavailable"
                                        font.pixelSize: Theme.fontSizeSmall
                                        color: Theme.surfaceVariantText
                                        elide: Text.ElideRight
                                    }

                                    StyledText {
                                        id: hideText
                                        text: "Hide"
                                        font.pixelSize: Theme.fontSizeSmall
                                        font.weight: Font.Medium
                                        color: Theme.primary

                                        MouseArea {
                                            anchors.fill: parent
                                            anchors.margins: -Theme.spacingXS
                                            cursorShape: Qt.PointingHandCursor
                                            onClicked: root.hideProvider(modelData.provider)
                                        }
                                    }
                                }

                                StyledText {
                                    visible: modelData.status !== "error" && Boolean(modelData.warning)
                                    width: parent.width
                                    text: modelData.warning || ""
                                    font.pixelSize: Theme.fontSizeSmall - 1
                                    color: Theme.surfaceVariantText
                                    elide: Text.ElideRight
                                }

                                Repeater {
                                    model: modelData.status === "error" ? [] : (modelData.windows || [])

                                    delegate: Column {
                                        required property var modelData
                                        width: providerColumn.width
                                        spacing: 3
                                        property real pct: root.percentage(Number(modelData.used) || 0, Number(modelData.limit) || 0)

                                        Row {
                                            width: parent.width

                                            StyledText {
                                                width: parent.width / 2
                                                text: root.windowLabel(modelData)
                                                font.pixelSize: Theme.fontSizeSmall
                                                color: Theme.surfaceVariantText
                                            }

                                            StyledText {
                                                width: parent.width / 2
                                                text: pct >= 0 ? Math.round(pct) + "% used" : root.formatValue(modelData.used, modelData.unit)
                                                font.pixelSize: Theme.fontSizeSmall
                                                color: root.usageColor(pct)
                                                horizontalAlignment: Text.AlignRight
                                            }
                                        }

                                        Rectangle {
                                            width: parent.width
                                            height: 5
                                            radius: height / 2
                                            color: Theme.surfaceContainerHighest

                                            Rectangle {
                                                width: pct >= 0 ? Math.min(parent.width, parent.width * pct / 100) : 0
                                                height: parent.height
                                                radius: parent.radius
                                                color: root.usageColor(pct)
                                            }
                                        }

                                        StyledText {
                                            visible: text !== ""
                                            text: root.resetText(modelData.reset_at)
                                            font.pixelSize: Theme.fontSizeSmall - 1
                                            color: Theme.surfaceVariantText
                                        }
                                    }
                                }
                            }
                        }
                    }

                    Row {
                        width: parent.width
                        height: 36
                        spacing: Theme.spacingS

                        Rectangle {
                            width: (parent.width - parent.spacing) / 2
                            height: parent.height
                            radius: Theme.cornerRadius
                            color: refreshArea.containsMouse ? Theme.surfaceContainerHighest : Theme.surfaceContainerHigh

                            Row {
                                anchors.centerIn: parent
                                spacing: Theme.spacingXS

                                DankIcon {
                                    name: "refresh"
                                    size: Theme.iconSize - 4
                                    color: root.loading ? Theme.surfaceVariantText : Theme.primary
                                    anchors.verticalCenter: parent.verticalCenter
                                }

                                StyledText {
                                    text: root.loading ? "Refreshing…" : "Refresh"
                                    font.pixelSize: Theme.fontSizeSmall
                                    font.weight: Font.Medium
                                    color: root.loading ? Theme.surfaceVariantText : Theme.primary
                                    anchors.verticalCenter: parent.verticalCenter
                                }
                            }

                            MouseArea {
                                id: refreshArea
                                anchors.fill: parent
                                hoverEnabled: true
                                enabled: !root.loading
                                cursorShape: Qt.PointingHandCursor
                                onClicked: root.refreshNow()
                            }
                        }

                        Rectangle {
                            width: (parent.width - parent.spacing) / 2
                            height: parent.height
                            radius: Theme.cornerRadius
                            color: settingsArea.containsMouse ? Theme.surfaceContainerHighest : Theme.surfaceContainerHigh

                            Row {
                                anchors.centerIn: parent
                                spacing: Theme.spacingXS

                                DankIcon {
                                    name: "settings"
                                    size: Theme.iconSize - 4
                                    color: Theme.primary
                                    anchors.verticalCenter: parent.verticalCenter
                                }

                                StyledText {
                                    text: "Settings"
                                    font.pixelSize: Theme.fontSizeSmall
                                    font.weight: Font.Medium
                                    color: Theme.primary
                                    anchors.verticalCenter: parent.verticalCenter
                                }
                            }

                            MouseArea {
                                id: settingsArea
                                anchors.fill: parent
                                hoverEnabled: true
                                cursorShape: Qt.PointingHandCursor
                                onClicked: {
                                    root.showingSettings = true
                                    root.fetchConfig()
                                }
                            }
                        }
                    }
                }
            }

            Column {
                visible: root.showingSettings
                width: parent.width
                spacing: Theme.spacingM

                Row {
                    width: parent.width
                    height: 32
                    spacing: Theme.spacingS

                    Rectangle {
                        width: 62
                        height: parent.height
                        radius: Theme.cornerRadius
                        color: backArea.containsMouse ? Theme.surfaceContainerHighest : Theme.surfaceContainerHigh

                        StyledText {
                            anchors.centerIn: parent
                            text: "← Back"
                            font.pixelSize: Theme.fontSizeSmall
                            font.weight: Font.Medium
                            color: Theme.primary
                        }

                        MouseArea {
                            id: backArea
                            anchors.fill: parent
                            hoverEnabled: true
                            cursorShape: Qt.PointingHandCursor
                            onClicked: root.showingSettings = false
                        }
                    }

                    StyledText {
                        text: "Provider settings"
                        font.pixelSize: Theme.fontSizeMedium
                        font.weight: Font.Medium
                        color: Theme.surfaceText
                        anchors.verticalCenter: parent.verticalCenter
                    }
                }

                StyledText {
                    width: parent.width
                    text: "Choose credentials, then choose one provider for the small panel indicator."
                    font.pixelSize: Theme.fontSizeSmall
                    color: Theme.surfaceVariantText
                    wrapMode: Text.WordWrap
                }

                Repeater {
                    model: root.providerOptions

                    delegate: Rectangle {
                        id: providerRow
                        required property var modelData
                        property var option: modelData
                        property bool active: root.providerSettings[option.providerId] !== false
                        property bool hasSources: option.sources !== undefined
                        width: parent.width
                        height: hasSources ? 78 : 56
                        radius: Theme.cornerRadius
                        color: Theme.surfaceContainerHigh

                        Column {
                            anchors.left: parent.left
                            anchors.right: controls.left
                            anchors.verticalCenter: parent.verticalCenter
                            anchors.leftMargin: Theme.spacingM
                            anchors.rightMargin: Theme.spacingS
                            spacing: 2

                            StyledText {
                                text: providerRow.option.label
                                font.pixelSize: Theme.fontSizeSmall
                                font.weight: Font.Medium
                                color: providerRow.active ? Theme.surfaceText : Theme.surfaceVariantText
                            }

                            StyledText {
                                visible: !providerRow.hasSources
                                width: parent.width
                                text: providerRow.option.source
                                font.pixelSize: Theme.fontSizeSmall - 1
                                color: Theme.surfaceVariantText
                                elide: Text.ElideRight
                            }

                            Row {
                                visible: providerRow.hasSources
                                spacing: Theme.spacingXS

                                Repeater {
                                    model: providerRow.option.sources || []

                                    delegate: Rectangle {
                                        required property var modelData
                                        property bool selected: root.providerSource(providerRow.option) === modelData.value
                                        width: sourceLabel.implicitWidth + Theme.spacingS * 2
                                        height: 20
                                        radius: height / 2
                                        color: selected ? Theme.primary : Theme.surfaceContainerHighest

                                        StyledText {
                                            id: sourceLabel
                                            anchors.centerIn: parent
                                            text: modelData.label
                                            font.pixelSize: Theme.fontSizeSmall - 1
                                            font.weight: Font.Medium
                                            color: selected ? Theme.surface : Theme.surfaceVariantText
                                        }

                                        MouseArea {
                                            anchors.fill: parent
                                            cursorShape: Qt.PointingHandCursor
                                            onClicked: root.setProviderSource(providerRow.option.providerId, modelData.value)
                                        }
                                    }
                                }
                            }
                        }

                        Row {
                            id: controls
                            anchors.right: parent.right
                            anchors.rightMargin: Theme.spacingM
                            anchors.verticalCenter: parent.verticalCenter
                            spacing: Theme.spacingS

                            StyledText {
                                text: root.pinnedProvider === providerRow.option.providerId ? "Showing" : "Show"
                                font.pixelSize: Theme.fontSizeSmall - 1
                                font.weight: Font.Medium
                                color: Theme.primary

                                MouseArea {
                                    anchors.fill: parent
                                    anchors.margins: -Theme.spacingXS
                                    cursorShape: Qt.PointingHandCursor
                                    onClicked: root.setPinnedProvider(root.pinnedProvider === providerRow.option.providerId ? "" : providerRow.option.providerId)
                                }
                            }

                            Rectangle {
                                width: 36
                                height: 20
                                radius: height / 2
                                color: providerRow.active ? Theme.primary : Theme.surfaceContainerHighest

                                Rectangle {
                                    width: 14
                                    height: 14
                                    radius: width / 2
                                    x: providerRow.active ? parent.width - width - 3 : 3
                                    anchors.verticalCenter: parent.verticalCenter
                                    color: Theme.surface
                                }

                                MouseArea {
                                    anchors.fill: parent
                                    cursorShape: Qt.PointingHandCursor
                                    onClicked: root.setProviderEnabled(providerRow.option.providerId, !providerRow.active)
                                }
                            }
                        }
                    }
                }

                StyledText {
                    width: parent.width
                    text: root.pinnedProvider === "" ? "Small panel: tap Show on one provider" : "Small panel: " + root.providerName({ provider: root.pinnedProvider, label: root.pinnedProvider })
                    font.pixelSize: Theme.fontSizeSmall
                    color: Theme.surfaceVariantText
                }
            }
        }
    }

    popoutWidth: 360
    popoutHeight: settledPopoutHeight
}
