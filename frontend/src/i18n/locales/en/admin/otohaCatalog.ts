export default {
  otohaCatalog: {
    action: 'Catalog',
    title: 'Otoha Model Catalog',
    titleWithGroup: 'Otoha Model Catalog · {name}',
    intro: 'The Otoha app lists these models in this order, with their abilities, profile and price. With an empty catalog the app shows the group\'s usual model list.',
    groupRate: 'Group rate {rate}x',
    shownCount: '{count} models shown in the app',
    addPlaceholder: 'Model name, e.g. gpt-6-luna',
    add: 'Add model',
    refresh: 'Refresh',
    empty: 'No models in the catalog yet',
    emptyHint: 'Once you add a model, the Otoha app shows the models and prices listed here.',
    columns: {
      order: 'Order',
      model: 'Model',
      shown: 'Shown',
      salePrice: 'Price (input / output)',
      tier: 'Tier',
      status: 'Status',
      actions: 'Actions'
    },
    moveUp: 'Move up',
    moveDown: 'Move down',
    perMillion: 'per million tokens',
    upstreamPrice: 'Before rate {price}',
    noPrice: 'No price',
    status: {
      shown: 'Shown in the app',
      disabled: 'Turned off',
      not_allowed: 'Not allowed in group',
      no_account: 'No account for it',
      no_price: 'No price'
    },
    statusHint: {
      not_allowed: 'Add this model to the group\'s model allowlist to show it.',
      no_account: 'No account in the group can serve this model.',
      no_price: 'The price table has no price for this model. Set one in the group\'s per-model pricing.'
    },
    tier: {
      auto: 'Automatic (from price)',
      low: 'Low',
      standard: 'Standard',
      high: 'High'
    },
    deleteTitle: 'Remove model',
    deleteMessage: 'Remove {model} from the catalog? The app stops showing it after its next refresh.',
    saved: 'Saved',
    deleted: 'Removed',
    loadFailed: 'Could not load the catalog. Please try again later.',
    saveFailed: 'Could not save. Please try again later.',
    invalid: 'Something is filled in wrongly. Please check and save again.',
    gone: 'This model is no longer in the catalog. Please refresh.',
    deleteFailed: 'Could not remove the model. Please try again later.',
    orderFailed: 'Could not save the order; the previous order is back.',
    exists: 'This model is already in the catalog.',
    preview: {
      title: 'What the app receives',
      revision: 'Version {revision}',
      noModels: 'No model is shown in the app right now.',
      none: 'The catalog is empty, so the app shows the group\'s usual model list.',
      separator: ', ',
      abilities: 'Abilities',
      use: 'Use',
      image: 'Images',
      tools: 'Tools',
      context: 'Context {tokens}'
    },
    editor: {
      createTitle: 'Add model',
      editTitle: 'Edit model · {model}',
      basics: 'Basics',
      modelId: 'Model name (used by the app in requests)',
      name: 'Display name',
      description: 'Description',
      enabled: 'Show in the app',
      prefill: 'Fill from upstream',
      prefilling: 'Reading…',
      prefillDone: 'Filled in the upstream abilities. Check them before saving.',
      prefillMissing: 'Upstream has nothing on this model. Please fill it in by hand.',
      prefillFailed: 'Could not read upstream information. Please try again later.',
      abilities: 'Abilities',
      inputs: 'Accepts',
      input: {
        text: 'Text',
        image: 'Images',
        audio: 'Audio',
        video: 'Video',
        file: 'Files'
      },
      tools: 'Can call tools',
      context: 'Context (tokens)',
      maxOutput: 'Longest reply (tokens)',
      reasoning: 'Reasoning levels (lowest first, comma separated)',
      defaultReasoning: 'Default reasoning',
      notSet: 'Not set',
      profile: 'Model profile',
      speed: 'Speed',
      speeds: {
        fast: 'Fast',
        standard: 'Standard',
        slow: 'Slow'
      },
      complexity: 'Hardest work it suits',
      complexities: {
        simple: 'Simple',
        medium: 'Medium',
        complex: 'Complex'
      },
      roles: 'Suited roles',
      roleOptions: {
        lead: 'Lead (understands the request, splits the work)',
        execute: 'Execute (does one task)'
      },
      strengths: 'Strength by area',
      domains: {
        planning: 'Planning',
        writing: 'Writing',
        coding: 'Coding',
        research: 'Research and web',
        data: 'Data and sheets',
        summarize: 'Summaries',
        vision: 'Images',
        translation: 'Translation'
      },
      levels: {
        unknown: 'Unknown',
        strong: 'Strong',
        usable: 'Usable',
        avoid: 'Avoid'
      },
      use: 'Use',
      useHint: 'Leave empty to derive it from the profile above.',
      uses: {
        default: 'Everyday',
        writing: 'Writing',
        planning: 'Planning',
        fast: 'Quick',
        summarize: 'Summaries',
        deep: 'Deep work',
        coding: 'Coding',
        web: 'Web'
      },
      profileSource: 'Profile based on',
      profileSources: {
        vendor: 'Vendor information',
        evaluation: 'Our evaluation',
        admin: 'Own judgement'
      },
      price: 'Price',
      saleNow: 'Price shown in the app: {price}',
      upstreamNow: 'Price in the group (before the rate): {price}',
      noUpstreamPrice: 'The price table has no price for this model.',
      priceHint: 'Price = the model\'s price in the group (the group\'s per-model pricing, else the channel price or the built-in price table) × group rate, the same as what is charged. To change it, use the group\'s per-model pricing.',
      tier: 'Tier',
      required: 'Please enter the model name.',
      modelIdInvalid: 'The model name cannot contain spaces and has at most 200 characters.',
      tooLong: 'The display name has at most 200 characters, the description at most 2000.',
      tokensInvalid: 'Context and longest reply must be whole numbers from 0 to 100 million.',
      reasoningInvalid: 'Each reasoning level is one word, such as low, medium, high.',
      defaultNotInList: 'The default reasoning must be one of the reasoning levels.'
    }
  }
}
