---
title: PassCtrl Documentation
description: Staged operator and configuration guides for the PassCtrl forward-auth runtime.
permalink: /
---

# Welcome

PassCtrl ships with modular agents for configuration, admission, rule execution, and response shaping. The documentation is staged so operators can move from deployment to day-two operations without wading through contributor notes.

## How to Use These Docs

1. Work through the stages in order the first time you deploy PassCtrl.
2. Revisit specific guides as you tune endpoint and rule behavior.
3. Keep the appendices handy for development workflows and design references.

## Documentation Stages

{% assign stages = site.data.stages %}
<ul class="stage-grid">
  {% for stage in stages %}
    <li class="stage-card">
      <h3><a href="{{ stage.overview_url | relative_url }}">{{ stage.title }}</a></h3>
      <p>{{ stage.summary }}</p>
      <ul>
        {% for link in stage.links %}
          <li><a href="{{ link.url | relative_url }}">{{ link.title }}</a></li>
        {% endfor %}
      </ul>
    </li>
  {% endfor %}
</ul>

## Looking for Developer Guides?

Contributor-oriented workflows now live in the [Development Workflow appendix]({{ '/development/contributor/' | relative_url }}). Architectural deep dives remain in `design/` for engineers extending the runtime.
