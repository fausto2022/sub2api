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
    description: 'Browse models and pricing by type and group.',
    empty: 'No models are available yet.',
    noSearchResult: 'No models match your filters.',
    emptyGroups: 'Groups without models',
    clearFilters: 'Clear filters',
    retry: 'Try again',
    types: {
      all: 'All',
      text: 'Text',
      image: 'Image',
      video: 'Video',
      audio: 'Audio',
      embedding: 'Embedding',
      other: 'Other'
    },
    filters: {
      ...dashboard.modelPlaza.filters,
      typeLabel: 'Model type',
      allGroups: 'All groups'
    },
    table: {
      ...dashboard.modelPlaza.table,
      peakPriceHint: 'The per-model peak price shown includes the effective group or personal rate.',
      referenceMetric: '{priceSource}: {metric}',
      tokenPriceLabel: '{metric} ({unit})',
      pricePair: 'Input / output'
    }
  },
  ...channelMonitorV2,
  ...batchImage,
  ...tickets,
  // Admin namespace additions for the model pricing page (admin.modelPricing.*)
  // and the ops management pages (admin.opsmgmt.*).
  admin: { ...admin, modelPricing: modelPricing.modelPricing, opsmgmt: opsmgmt.opsmgmt },
  ...misc,
  // The tickets and model pricing modules also ship nav additions
  // (nav.tickets / nav.ticketManagement / nav.modelPricing); merge them with
  // common.nav explicitly so object spread does not drop existing labels.
  // Ops management adds nav.opsMgmt* entries the same way.
  nav: { ...common.nav, ...tickets.nav, ...modelPricing.nav, ...opsmgmt.nav },
}
