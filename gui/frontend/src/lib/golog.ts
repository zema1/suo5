/** @type LanguageFn */
export default function CliLog(_hljs) {
    // 定义可重用的规则，用于嵌套
    const URL_RULE = {
        className: 'link',
        begin: /(https?:\/\/|ftps?:\/\/|www\.)[^\s<>"']+/
    };

    const IP_ADDRESS_RULE = {
        className: 'number',
        begin: /\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(:\d{1,5})?\b/
    };

    return {
        name: 'My App Log',
        case_insensitive: false,
        contains: [
            // 规则 1：高亮 [ERRO] 标签 (保持不变)
            {
                className: 'deletion', // 通常为红色
                begin: /\[ERRO\]/
            },

            // 规则 2：高亮 [INFO] 标签 (保持不变)
            {
                className: 'addition', // 通常为绿色
                begin: /\[INFO\]/
            },

            // 规则 3：高亮 [DBUG] 的整行 (★★ 更新后的规则 ★★)
            {
                className: 'comment', // 整行应用 'keyword' 样式 (通常为黄色/橙色)
                begin: /\[DBUG\]/,    // 匹配以 [DBUG] 开始
                end: /$/,             // 匹配到行尾结束
                contains: [
                    // 在 DBUG 行内部，我们仍然希望高亮 URL 和 IP
                    URL_RULE,
                    IP_ADDRESS_RULE
                ]
            },

            // 规则 4 & 5：为非 DBUG 行高亮 URL 和 IP 地址
            // 这两个规则需要保留在顶层，以处理 INFO 和 ERRO 行中的 URL/IP
            URL_RULE,
            IP_ADDRESS_RULE
        ]
    };
}