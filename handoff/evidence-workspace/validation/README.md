# v1.1 交接包验证记录

本目录只验证交接材料，不验证目标项目运行或视觉实现。

- `package-validation.txt`：实际运行 scripts/validate_handoff.py 的输出，含8夹具、24任务、Markdown链接、来源和清单。
- `screen-validation.txt`：实际运行 scripts/validate_screens.py 的输出，含7张不同原图的PNG CRC、SHA-256、尺寸、来源、任务绑定和离线图集链接。
- `typescript-typecheck.txt`：对原领域契约的严格类型检查，不是应用构建。
- `environment.json`：本次实际材料验证环境。
- `baseline-v1.0-inventory.json`：原ZIP的41文件清单与哈希；旧包只有1张PNG。
- `SHA256SUMS.txt`：覆盖包内全部文件，但排除自身及会在运行时重写的package-validation.txt、screen-validation.txt，避免循环依赖。

图片在打包时已用Pillow解码读取，并保持与本对话附件原始字节一致。HTML图集仅检查了本地链接和图片映射，没有做浏览器渲染验收。没有执行目标仓库测试；原始审查repro的历史PASS仍只表示缺陷复现。

在包根目录运行：

```bash
python scripts/validate_screens.py
python scripts/validate_handoff.py
```

第二个脚本需要jsonschema；缺失时应按提示安装到隔离环境，不把跳过写成通过。修改材料后需重新生成哈希清单再验证。
