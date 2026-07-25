# env配置文件说明

‍

配置文件说明:  
🔧 基础配置

|配置项|默认值|说明|
| ---------------------------------| ------------| ---------------------------------|
|KVM\_DEVELOPMENT\_MODE|true|开发模式（生产环境改为 false）|
|KVM\_BROWSER\_TITLE|CVMConsole|浏览器标签页标题|
|KVM\_HOME\_TITLE|CVMConsole|首页标题|
|KVM\_SITE\_TITLE|CVMConsole|站点标题|
|KVM\_PRODUCT\_NAME|IAAS平台|产品名称|
|KVM\_HOST\_IP| *(空)*|宿主机 IP（公网访问时需要填写）|
|KVM\_PUBLIC\_BASE\_URL| *(空)*|公网基础 URL|

🗄️ 数据库配置

|配置项|默认值|说明|
| -------------------------| -----------------| ----------------------------|
|KVM\_DB\_TYPE|mysql|数据库类型（mysql/sqlite）|
|KVM\_DB\_HOST|10.100.5.99|数据库主机地址|
|KVM\_DB\_PORT|31306|数据库端口|
|KVM\_DB\_USERNAME|root|数据库用户名|
|KVM\_DB\_PASSWORD|Tingyu@1234|数据库密码|
|KVM\_DB\_DATABASE|kvm\_console|数据库名称|

📁 目录配置

|配置项|默认值|说明|
| -------------------------------------| -----------------------------------| -----------------------|
|KVM\_CLONE\_DIR|/var/lib/libvirt/images|VM 磁盘克隆目录|
|KVM\_ISO\_DIR|/var/lib/libvirt/images/ISO|ISO 镜像目录|
|KVM\_TEMPLATE\_DIR|/var/lib/libvirt/images/templates|模板存储目录|
|KVM\_TEMPLATE\_EXPORT\_DIR|...templates/\_exports|模板导出目录|
|KVM\_TEMPLATE\_IMPORT\_DIR|...templates/\_imports|模板导入目录|
|KVM\_RESCUE\_ISO| *(空)*|救援 ISO 路径（可选）|

🌐 网络配置

|配置项|默认值|说明|
| --------------------------------------------------| -------------| ----------------------------|
|KVM\_NETWORK\_BACKEND|ovs|网络后端（ovs/bridge/nat）|
|KVM\_DEFAULT\_NETWORK|default|默认网络名称|
|KVM\_SUBNET\_PREFIX|192.168.122|默认子网前缀|
|KVM\_EXTERNAL\_NIC| *(空)*|外部物理网卡名（如 eth0）|
|KVM\_NETWORK\_WAIT\_ONLINE\_DISABLED|false|启动时是否跳过等待网络上线|

🔌 OVS 配置（Open vSwitch

|配置项|默认值|说明|
| --------------------------------| --------| ------------------|
|KVM\_OVS\_BRIDGE|br-ovs|OVS 网桥名称|
|KVM\_OVS\_UPLINK| *(空)*|OVS 上行物理接口|
|KVM\_OVS\_DHCP\_START| *(空)*|OVS DHCP 起始 IP|
|KVM\_OVS\_DHCP\_END| *(空)*|OVS DHCP 结束 IP|

🔄 VPC 配置（私有云）

|配置项|默认值|说明|
| -----------------------------------| ---------------------------------| ------------------|
|KVM\_VPC\_SUBNET\_PREFIX|10.200|VPC 子网前缀|
|KVM\_VPC\_VLAN\_START|100|VLAN 起始 ID|
|KVM\_VPC\_VLAN\_END|4094|VLAN 结束 ID|
|KVM\_VPC\_DNS|223.5.5.5,223.6.6.6|VPC DNS 服务器|
|KVM\_VPC\_ACL\_TABLE|kvm\_console\_vpc\_acl|VPC ACL 数据库表|

🔐 安全配置

|配置项|默认值|说明|
| ---------------------------------------------------| ----------------------------------| ------------------------------------------|
|KVM\_JWT\_SECRET|test-secret-key-...|JWT 签名密钥（​**生产必须更换**）|
|KVM\_JWT\_SECRET\_ROTATE\_HOURS|24|JWT 密钥自动轮换间隔（小时，0\=禁用）|
|KVM\_SECURITY\_SECRET|a4uFTxYfI4...|安全模块密钥|
|KVM\_LEGACY\_SECURITY\_SECRET|kvm-console-secret-key-change-me|旧版安全密钥|
|KVM\_VM\_CREDENTIAL\_SECRET|4FNwQ4rZvb...|VM 凭据加密密钥|
|KVM\_SESSION\_FINGERPRINT\_ENABLED|true|是否启用会话指纹验证|
|KVM\_PASSWORD\_BREACH\_CHECK\_ENABLED|true|是否启用密码泄露检测|
|KVM\_REQUEST\_FILTER\_ENABLED|true|是否启用请求过滤|
|KVM\_HARDWARE\_PASSTHROUGH\_ENABLED|true|是否允许硬件直通（PCI）|

📧 SMTP 邮件配置

|配置项|默认值|说明|
| --------------------------------------| ------------------| --------------------------|
|KVM\_SMTP\_HOST|smtp.qq.com|SMTP 服务器地址|
|KVM\_SMTP\_PORT|465|SMTP 端口|
|KVM\_SMTP\_SECURITY|ssl|加密方式（ssl/tls/none）|
|KVM\_SMTP\_USERNAME|598941324@qq.com|SMTP 登录账号|
|KVM\_SMTP\_PASSWORD\_ENC|dao9jCVXNSf...|加密后的 SMTP 密码|
|KVM\_SMTP\_FROM\_ADDRESS|598941324@qq.com|发件人地址|
|KVM\_SMTP\_FROM\_NAME|CVMConsole|发件人名称|
|KVM\_SMTP\_TIMEOUT\_SECONDS|15|发送超时（秒）|

💾 日志配置

|配置项|默认值|说明|
| -------------------------------------| -----------------| -----------------------------------|
|KVM\_LOG\_DIR|./log|日志目录|
|KVM\_LOG\_LEVEL|info|日志级别（debug/info/warn/error）|
|KVM\_LOG\_CONSOLE|true|是否同时输出到控制台|
|KVM\_LOG\_CONSOLE\_LEVEL|warn|控制台日志级别|
|KVM\_LOG\_CONSOLE\_TYPES|app,cmd,libvirt|输出到控制台的日志类型|
|KVM\_LOG\_COMPRESS|true|是否压缩旧日志|
|KVM\_LOG\_MAX\_SIZE\_MB|100|单个日志文件最大（MB）|
|KVM\_LOG\_MAX\_BACKUPS|0|保留的旧日志文件数|
|KVM\_LOG\_MAX\_DAYS|7|日志最长保留天数|

⚡ 性能/配额配置

|配置项|默认值|说明|
| -------------------------------------------------| --------| --------------------------------------|
|KVM\_EXEC\_TIMEOUT\_SECONDS|30|命令执行超时（秒）|
|KVM\_AUTO\_PORT\_START|10000|自动分配端口起始值|
|KVM\_AUTO\_PORT\_END|20000|自动分配端口结束值|
|KVM\_BATCH\_CLONE\_MAX\_CONCURRENCY|10|批量克隆最大并发数|
|KVM\_DEFAULT\_DISK\_IOPS\_READ|0|默认磁盘 IO 读取限制（0\=不限制）|
|KVM\_DEFAULT\_DISK\_IOPS\_WRITE|0|默认磁盘 IO 写入限制|
|KVM\_DEFAULT\_DISK\_IOPS\_TOTAL|0|默认磁盘 IO 总限制|
|KVM\_MAX\_BURST\_INBOUND|0|入站流量突发上限|
|KVM\_MAX\_BURST\_OUTBOUND|0|出站流量突发上限|
|KVM\_USE\_GO\_LIBVIRT|true|使用 Go 原生 libvirt 绑定|

🧠 动态内存调度

|配置项|默认值|说明|
| -------------------------------------------------------------------| --------| --------------------------|
|KVM\_DYNAMIC\_MEMORY\_SCHEDULER\_ENABLED|true|是否启用动态内存调度|
|KVM\_DYNAMIC\_MEMORY\_INTERVAL\_SECONDS|30|检查间隔（秒）|
|KVM\_DYNAMIC\_MEMORY\_HOST\_RESERVE\_MB|2048|宿主机保留内存（MB）|
|KVM\_DYNAMIC\_MEMORY\_HOST\_RESERVE\_PERCENT|20|宿主机保留内存（百分比）|
|KVM\_DYNAMIC\_MEMORY\_INCREASE\_THRESHOLD\_PERCENT|15|触发扩容的内存使用率阈值|
|KVM\_DYNAMIC\_MEMORY\_RECLAIM\_THRESHOLD\_PERCENT|35|触发回收的内存使用率阈值|
|KVM\_DYNAMIC\_MEMORY\_COOLDOWN\_SECONDS|120|操作冷却时间（秒）|
|KVM\_DYNAMIC\_MEMORY\_OBSERVATION\_HOURS|24|历史数据观察窗口（小时）|

🔧 维护模式

|配置项|默认值|说明|
| -----------------------------------------------------------------| ------------------------------------------| --------------------------|
|KVM\_MAINTENANCE\_MODE|false|是否进入维护模式|
|KVM\_MAINTENANCE\_SERVICE\_UNITS|kvm-console.service,libvirtd.service,...|维护模式下停止的服务|
|KVM\_MAINTENANCE\_VM\_SHUTDOWN\_TIMEOUT\_SECONDS|40|维护时 VM 关机超时（秒）|

🔍 安全扫描

|配置项|默认值|说明|
| ----------------------------------------------------------------------| --------| -------------------------------|
|KVM\_PORT\_FORWARD\_HTTP\_PROBE\_ENABLED|true|是否启用端口转发 HTTP 探测|
|KVM\_PORT\_FORWARD\_HTTP\_PROBE\_INTERVAL\_MINUTES|60|探测间隔（分钟）|
|KVM\_PORT\_FORWARD\_HTTP\_PROBE\_TIMEOUT\_SECONDS|3|探测超时（秒）|
|KVM\_SCHEDULER\_EVENT\_RETENTION\_HOURS|168|调度事件保留时长（小时，7天）|
