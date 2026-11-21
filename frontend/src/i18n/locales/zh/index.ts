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
  modelPlaza: {
    ...dashboard.modelPlaza,
    description: '按类型和分组浏览模型与价格。',
    empty: '暂无可用模型。',
    noSearchResult: '没有符合筛选条件的模型。',
    emptyGroups: '暂未配置模型的分组',
    clearFilters: '清除筛选',
    retry: '重试',
    types: {
      all: '全部',
      text: '文本',
      image: '图像',
      video: '视频',
      audio: '音频',
      embedding: '嵌入',
      other: '其他'
    },
    filters: {
      ...dashboard.modelPlaza.filters,
      typeLabel: '模型类型',
      allGroups: '全部分组'
    },
    table: {
      ...dashboard.modelPlaza.table,
      peakPriceHint: '这里显示的模型峰时价已乘生效的分组倍率或专属倍率。',
      referenceMetric: '{priceSource}：{metric}',
      tokenPriceLabel: '{metric}（{unit}）',
      pricePair: '输入 / 输出'
    }
  },
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
