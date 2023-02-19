<template>
  <n-config-provider :theme-overrides="themeOverrides" :locale="zhCN" :date-locale="dateZhCN" :hljs="hljs">
    <n-message-provider>
      <Home/>
    </n-message-provider>
  </n-config-provider>
</template>

<script lang="ts" setup>
import {dateZhCN, GlobalThemeOverrides, zhCN} from "naive-ui";
import Home from "./Home.vue"
import hljs from 'highlight.js/lib/core'
import accesslog from 'highlight.js/lib/languages/accesslog'

hljs.registerLanguage("accesslog", accesslog)

const themeOverrides: GlobalThemeOverrides = {
  common: {
    primaryColorPressed: "#101010",
    primaryColor: "#464646",
    primaryColorHover: "#7A7B7AFF",
    primaryColorSuppl: "#7A7B7AFF",
  }
}
</script>

<style lang="less">
</style>