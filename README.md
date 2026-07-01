# monitorDNS
## 监控指定域名的A记录解析变化, 会统计A记录变化的频率,以及最大,最小,平均值, 并收集所有出现过的A记录

## 选项
* -d 选项 // 必须指定
* -i 间隔 // 单位秒
* -p 是否在控制台打印 // 默认不打印
* -t 记录类型（A / CNAME）// 默认 A
* -web 启动 Web 模式（SQLite + 登录 + 页面管理）
* -addr Web 监听地址 // 默认 127.0.0.1:8080
* -db SQLite 文件路径 // 默认 monitorDNS.db

## 示例
> monitorDNS.exe -d www.baidu.com
>
> 监控 CNAME：
> monitorDNS.exe -d www.baidu.com -t CNAME -i 30 -p
>
> Web 模式（本地访问）：
> monitorDNS.exe -web -addr 127.0.0.1:8080 -db monitorDNS.db
> Web 模式（公网访问）：
> monitorDNS.exe -web -addr 0.0.0.0:8080 -db monitorDNS.db
> 
> 默认会在运行目录生成一个log.txt文件, 输出域名的解析变化
>
> Web 模式说明：
> 1. 首次启动会自动创建一个账号（默认用户名 admin），随机生成密码，并写入运行目录的 admin_credentials.txt
> 2. 打开浏览器访问 http://127.0.0.1:8080 登录
> 3. 登录后可以添加域名、选择记录类型（A/CNAME）、选择检测周期（30s/1m/5m）
> 4. 解析记录与变更事件会写入 SQLite，可在页面查看表格与图表统计
> 
![JGD7JJTXSCS0X75RD{1Z2W4](https://user-images.githubusercontent.com/52809998/164955704-09ce9189-5cd4-4498-b3ac-0bf3ee6116eb.png)
![JGD7JJTXSCS0X75RD{1Z2W4](https://raw.githubusercontent.com/4kercc/monitorDNS/refs/heads/main/img/2026-7-1%2016-41-33.png)
![JGD7JJTXSCS0X75RD{1Z2W4](https://github.com/4kercc/monitorDNS/blob/main/img/2.png)

