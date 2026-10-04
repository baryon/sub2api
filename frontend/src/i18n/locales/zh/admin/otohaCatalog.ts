export default {
  otohaCatalog: {
    action: '模型目录',
    title: 'Otoha 模型目录',
    titleWithGroup: 'Otoha 模型目录 · {name}',
    intro: '这里的模型按顺序显示在 Otoha App 中，并带上能力、档案和售价。目录为空时，App 显示分组原有的模型列表。',
    groupRate: '分组倍率 {rate}x',
    shownCount: 'App 中显示 {count} 个模型',
    addPlaceholder: '输入模型名称，例如 gpt-6-luna',
    add: '添加模型',
    refresh: '刷新',
    empty: '目录里还没有模型',
    emptyHint: '添加第一个模型后，Otoha App 改为显示这里的模型和售价。',
    columns: {
      order: '顺序',
      model: '模型',
      shown: '显示',
      salePrice: '售价（输入 / 输出）',
      tier: '价位',
      status: '状态',
      actions: '操作'
    },
    moveUp: '上移',
    moveDown: '下移',
    perMillion: '每百万 tokens',
    upstreamPrice: '倍率前 {price}',
    noPrice: '没有价格',
    status: {
      shown: '在 App 中显示',
      disabled: '已停用',
      not_allowed: '分组不允许',
      no_account: '没有可用账号',
      no_price: '没有价格'
    },
    statusHint: {
      not_allowed: '把这个模型加入分组的模型白名单后才会显示。',
      no_account: '分组里没有能处理这个模型的账号。',
      no_price: '价格表里没有这个模型的价格。请在分组设置的「分组逐模型定价」里为它设价格。'
    },
    tier: {
      auto: '自动（按售价）',
      low: '低',
      standard: '标准',
      high: '高'
    },
    deleteTitle: '删除模型',
    deleteMessage: '从目录中删除 {model}？App 下次刷新后不再显示它。',
    saved: '已保存',
    deleted: '已删除',
    loadFailed: '目录加载失败，请稍后重试。',
    saveFailed: '保存失败，请稍后重试。',
    invalid: '有内容填写得不对，请检查后再保存。',
    gone: '这个模型已经不在目录里了，请刷新。',
    deleteFailed: '删除失败，请稍后重试。',
    orderFailed: '顺序保存失败，已恢复原来的顺序。',
    exists: '这个模型已经在目录中。',
    preview: {
      title: 'App 收到的目录',
      revision: '版本 {revision}',
      noModels: '目前没有模型会显示在 App 中。',
      none: '目录为空，App 显示分组原有的模型列表。',
      separator: '、',
      abilities: '能力',
      use: '用途',
      image: '看图',
      tools: '工具',
      context: '上下文 {tokens}'
    },
    editor: {
      createTitle: '添加模型',
      editTitle: '编辑模型 · {model}',
      basics: '基本信息',
      modelId: '模型名称（App 请求时使用）',
      name: '显示名称',
      description: '说明',
      enabled: '在 App 中显示',
      prefill: '从上游填入',
      prefilling: '正在读取…',
      prefillDone: '已填入上游的能力信息，请检查后保存。',
      prefillMissing: '上游没有这个模型的信息，请手动填写。',
      prefillFailed: '读取上游信息失败，请稍后重试。',
      abilities: '能力',
      inputs: '可接收的内容',
      input: {
        text: '文字',
        image: '图片',
        audio: '音频',
        video: '视频',
        file: '文件'
      },
      tools: '可调用工具',
      context: '上下文（tokens）',
      maxOutput: '最长输出（tokens）',
      reasoning: '推理强度（由低到高，逗号分隔）',
      defaultReasoning: '默认推理强度',
      notSet: '不设置',
      profile: '模型档案',
      speed: '速度',
      speeds: {
        fast: '快',
        standard: '一般',
        slow: '慢'
      },
      complexity: '能胜任的复杂度',
      complexities: {
        simple: '简单',
        medium: '中等',
        complex: '复杂'
      },
      roles: '适合的角色',
      roleOptions: {
        lead: '主控（理解需求、拆分任务）',
        execute: '执行（完成单个任务）'
      },
      strengths: '各领域表现',
      domains: {
        planning: '策划与拆解',
        writing: '写作',
        coding: '代码',
        research: '调研与网页',
        data: '数据与表格',
        summarize: '总结与整理',
        vision: '看图',
        translation: '翻译'
      },
      levels: {
        unknown: '未知',
        strong: '擅长',
        usable: '可用',
        avoid: '不推荐'
      },
      use: '用途',
      useHint: '不选时按上面的档案自动推出。',
      uses: {
        default: '日常',
        writing: '写作',
        planning: '规划',
        fast: '快速',
        summarize: '摘要',
        deep: '深度',
        coding: '代码',
        web: '网页'
      },
      profileSource: '档案依据',
      profileSources: {
        vendor: '厂商说明',
        evaluation: '我们的评测',
        admin: '人工判断'
      },
      price: '价格',
      saleNow: 'App 显示的售价：{price}',
      upstreamNow: '分组价格（倍率前）：{price}',
      noUpstreamPrice: '价格表里没有这个模型的价格。',
      priceHint: '售价 = 这个模型在分组里的价格（分组逐模型定价，没有时用渠道价格或内置价格表）× 分组倍率，与实际扣费一致。要改价格，请在分组设置的「分组逐模型定价」里设置。',
      tier: '价位',
      required: '请填写模型名称。',
      modelIdInvalid: '模型名称不能有空格，最多 200 个字符。',
      tooLong: '显示名称最多 200 个字符，说明最多 2000 个字符。',
      tokensInvalid: '上下文和最长输出需要填 0 到 1 亿之间的整数。',
      reasoningInvalid: '推理强度每项是一个英文单词，例如 low、medium、high。',
      defaultNotInList: '默认推理强度需要在推理强度里。'
    }
  }
}
