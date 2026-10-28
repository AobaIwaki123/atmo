# atmo システム全体アーキテクチャとリポジトリ構成

本ドキュメントは、気圧監視・気象病対策ツール「atmo」におけるシステム全体の構成、リポジトリ間の役割分担、通信フロー、およびデプロイの関係性を整理したものです。

---

## 1. システム全体概要

`atmo` プロジェクトは、以下の 2 つのリポジトリが連携して構成されています。

```text
[ 外部気象 API (Open-Meteo) ]
            ▲
            │ 1時間ごとのデータ取得 & キャッシュ
            ▼
[ atmo (Go バックエンド / 自宅 k8s) ] ──(定期通知)──> [ Discord Webhook ]
      │                         │
      │ Web ホスティング        │ HTTP REST API (/api/v1/pressure)
      ▼                         ▼
[ ブラウザ (Web) ]       [ atmo-app (Flutter / クライアント) ]
                                ├─ iOS アプリ (TestFlight / App Store)
                                ├─ Android アプリ (野良 APK)
                                └─ ホーム画面ウィジェット (iOS / Android)
```

---

## 2. リポジトリごとの厳密な役割分担

| リポジトリ | 主な役割 | 技術スタック | デプロイ・稼働先 |
|---|---|---|---|
| **`atmo`**<br>(バックエンド & Webサーバー) | ・Open-Meteo 気圧データの空間キャッシュ API<br>・Discord への定期気圧アラート通知 (CronJob)<br>・Flutter Web ビルド成果物のホスティング | Go (Golang) | 自宅 Kubernetes<br>(`https://atmo.aooba.net`) |
| **`atmo-app`**<br>(フロントエンド / アプリ本体) | ・iOS / Android / Web 向けの共通 UI 描画<br>・24時間気圧推移グラフ (`fl_chart`)<br>・ホーム画面ウィジェット (WidgetKit / AppWidget)<br>・プライバシー保護（おおよその位置情報の取得） | Flutter (Dart)<br>Swift (iOS ウィジェット)<br>Kotlin (Android ウィジェット) | App Store / TestFlight<br>GitHub Releases (APK) |

---

## 3. よくある誤解と技術的事実

### Q1. アプリ（`atmo-app`）は Go の CLI コマンドを実行しているのか？
**A. 実行していません。**
スマホアプリやブラウザのサンドボックス制約上、端末内で外部の Go CLI バイナリを実行することはできません。
アプリはすべて **HTTP 通信（REST API: `GET https://atmo.aooba.net/api/v1/pressure`）** を経由して Go サーバーから JSON データを受信しています。

### Q2. `atmo-app` は Kotlin で作られているのか？
**A. アプリ本体の 95% 以上は Flutter (Dart) で作られています。**
画面レイアウト、グラフ描画、ボタン、状態管理、API 通信などはすべて `lib/` 配下の Dart コードです。
Kotlin は、Android のホーム画面ウィジェットを OS に認識させるためのネイティブ連携コード（`android/app/.../AtmoSmallWidgetProvider.kt`）においてピンポイントで利用されています（iOS 側では Swift が使われています）。

### Q3. `https://atmo.aooba.net/` は何のサーバーなのか？
**A. Go 製の `atmo` が、Flutter Web の成果物を配信している Web サーバーです。**
`atmo-app` で `flutter build web` した成果物を、Go の `//go:embed` 機能を用いて Go のシングルバイナリに埋め込み、Cloudflare Tunnel Ingress 経由で公開しています。

---

## 4. 各プラットフォームのデプロイと更新フロー

1. **iOS アプリ (TestFlight / App Store)**:
   - `atmo-app` 内で `flutter build ipa --release` を実行し、Transporter や Xcode から App Store Connect へアップロードします（`atmo` リポジトリは関与しません）。
2. **Android アプリ (野良 APK)**:
   - `atmo-app` でバンプしたバージョンタグ（`git tag v1.X.X`）を Push すると、GitHub Actions CI が APK を自動ビルドして GitHub Releases に公開します。
3. **Web 版 (`https://atmo.aooba.net`)**:
   - `atmo-app` で `flutter build web` を実行し、生成された静的ファイルを `atmo/web/` にコピーしてコミット & Push すると、`atmo` の CI（Docker & k8s）により自動デプロイされます。

---

## 5. プロジェクトの歴史的経緯

1. **原点 (CLI ツール時代)**:
   - 当初、`atmo` はターミナル上で 2D ASCII アートの気圧グラフを表示する Go 製のシンプルな CLI ツールとして誕生しました。
2. **Discord 通知の追加**:
   - 3時間ごとに気圧急変を検知し、Discord へ PNG グラフ付きで通知する CronJob 運用を開始しました。
3. **API サーバー化と Web 組み込み**:
   - スマホやブラウザからリアルタイムに確認できるようにするため、Go バイナリに HTTP サーバー機能（`-serve`）と Web ホスティング機能を追加しました。
4. **UI 専用リポジトリ（`atmo-app`）の独立**:
   - ホーム画面ウィジェットや高品質なネイティブ UI を実現するため、Flutter クライアントとして `atmo-app` を分離・新設しました。
