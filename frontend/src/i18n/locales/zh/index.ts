import adminAccessKeys from './adminAccessKeys'
import mcp from './mcp'
import landing from './landing'
import common from './common'
import dashboard from './dashboard'
import channelMonitorV2 from './channelMonitorV2'
import batchImage from './batchImage'
import tickets from './tickets'
import admin from './admin'
import modelPricing from './admin/modelPricing'
import opsmgmt from './admin/opsmgmt'
import misc from './misc'

export default {
  adminAccessKeys,
  mcp,
  ...landing,
  ...common,
  ...dashboard,
  ...channelMonitorV2,
  ...batchImage,
  ...tickets,
  // 模型价格页的 admin 命名空间增补键（admin.modelPricing.*）。
  // 运营管理页同理（admin.opsmgmt.*）。
  admin: { ...admin, modelPricing: modelPricing.modelPricing, opsmgmt: opsmgmt.opsmgmt },
  ...misc,
  // 工单/模型价格模块自带 nav 增补键（nav.tickets / nav.ticketManagement / nav.modelPricing），
  // 需与 common.nav 显式合并，避免对象展开覆盖掉既有导航文案。
  // 运营管理同理（nav.opsMgmt / nav.opsMgmtDashboard / nav.opsMgmtSites / nav.opsMgmtExpand / nav.opsMgmtProfit）。
  nav: { ...common.nav, ...tickets.nav, ...modelPricing.nav, ...opsmgmt.nav },
}
