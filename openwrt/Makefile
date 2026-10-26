include $(TOPDIR)/rules.mk

PKG_NAME:=mqtt-switchboard
PKG_VERSION:=1.0.0
PKG_RELEASE:=1

PKG_SOURCE_PROTO:=git
PKG_SOURCE_URL:=https://github.com/your-repo/mqtt-switchboard.git
PKG_SOURCE_VERSION:=v$(PKG_VERSION)

PKG_LICENSE:=MIT
PKG_MAINTAINER:=Mushi Developer <dev@example.com>

PKG_BUILD_DEPENDS:=golang/host
PKG_BUILD_PARALLEL:=1

GO_PKG:=github.com/your-repo/mqtt-switchboard
GO_PKG_BUILD_PKG:=github.com/your-repo/mqtt-switchboard/cmd/mqtt-switchboard
GO_PKG_LDFLAGS_X:=main.version=$(PKG_VERSION)

include $(INCLUDE_DIR)/package.mk
include ../../lang/golang/golang-package.mk

define Package/mqtt-switchboard
  SECTION:=net
  CATEGORY:=Network
  SUBMENU:=Web Servers/Proxies
  TITLE:=Ultra-lightweight Web-to-MQTT switchboard for OpenWrt
  DEPENDS:=$(GO_ARCH_DEPENDS)
  USERID:=root:root
endef

define Package/mqtt-switchboard/description
  mqtt-switchboard is a production-ready, ultra-lightweight Web-to-MQTT control panel
  specifically built for OpenWrt routers. Features Home Assistant MQTT discovery,
  dual-mode UI (Dark Glassmorphism + E-Ink high contrast), anti-bruteforce security,
  and physical relay debounce protection with RAM usage under 15MB.
endef

define Package/mqtt-switchboard/conffiles
/etc/config/mqtt-switchboard
/etc/mqtt-switchboard/config.json
endef

define Package/mqtt-switchboard/install
	$(call GoPackage/Package/Install/Bin,$(PKG_INSTALL_DIR))

	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) $(PKG_INSTALL_DIR)/usr/bin/mqtt-switchboard $(1)/usr/bin/

	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) ./files/mqtt-switchboard.init $(1)/etc/init.d/mqtt-switchboard

	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_CONF) ./files/mqtt-switchboard.config $(1)/etc/config/mqtt-switchboard

	$(INSTALL_DIR) $(1)/etc/mqtt-switchboard
	$(INSTALL_CONF) ./files/config.json $(1)/etc/mqtt-switchboard/config.json
endef

$(eval $(call GoPackage,mqtt-switchboard))
$(eval $(call BuildPackage,mqtt-switchboard))
