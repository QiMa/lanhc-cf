// 返回之前的网站
function goBack() {
    // 使用 replace 方法替换当前历史记录
    window.location.replace(document.referrer)
}