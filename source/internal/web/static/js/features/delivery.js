// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

/**
 * Delivery UI — entry point.
 *
 * The feature used to be one ~6 600-line object literal. It is now six files, one
 * per surface, each assigning its methods onto the same
 * `window.Features.delivery`; this file exists so anything that loads
 * `features/delivery.js` keeps working.
 *
 * Load order matters and is expressed in index.html: core first (it defines the
 * router and the helpers the others call), then the surfaces, then the test seam.
 *
 * The test seam lives here rather than in core.js so `node --test` has one stable
 * import path and the surface files stay free of CommonJS.
 */
if (typeof module !== "undefined" && module.exports) {
  module.exports = {
    parseRoute: (...a) => window.Features.delivery.parseRoute(...a),
    parseRoadmapMsBind: (...a) => window.Features.delivery.parseRoadmapMsBind(...a),
    parseGateChecklist: (...a) => window.Features.delivery.parseGateChecklist(...a),
    patchChecklistItem: (...a) => window.Features.delivery.patchChecklistItem(...a),
    composeDesignMarkdown: (...a) => window.Features.delivery.composeDesignMarkdown(...a),
    campaignPassReady: (...a) => window.Features.delivery.campaignPassReady(...a),
    parseCasePaste: (...a) => window.Features.delivery.parseCasePaste(...a),
    lifecycleLabel: (...a) => window.AnvilUI.lifecycleLabel(...a),
    badgeClass: (...a) => window.AnvilUI.badgeClass(...a),
    esc: (...a) => window.AnvilUI.esc(...a),
  };
}
