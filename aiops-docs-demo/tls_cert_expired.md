# 证书过期处置

## 现象

TLS 握手失败，客户端报证书过期，告警 `CertExpired` firing。

## 排查

1. 看有效期：`echo | openssl s_client -connect <host>:443 2>/dev/null | openssl x509 -noout -dates`。
2. 看链：确认是叶子证书过期还是中间证书缺失（`openssl s_client -showcerts`）。
3. 看续期：cert-manager 的 Certificate 资源 `kubectl describe certificate`，查自动续期为何没触发。

## 处置

- 短期：手动签发/更新证书并 reload（nginx `nginx -s reload`），恢复握手。
- 长期：cert-manager 配自动续期 + 到期前 30/7 天两级告警，禁止手动管证书。
