# ソラケアの第三者サービス表示

## Open-Meteo

ソラケアの気圧データは、[Open-Meteo](https://open-meteo.com/)から取得します。

表示する帰属情報:

> Weather data by [Open-Meteo.com](https://open-meteo.com/), licensed under [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/).

Webサーバーは47都道府県の代表地点だけを対象に、1時間ごとに順番にデータを更新します。利用者のリクエストではOpen-Meteoを直接呼び出しません。広告・サブスクリプション・有料機能を追加する場合は、無料APIの利用条件と契約プランを再確認します。

## OpenStreetMap / Nominatim

現在のWebサーバーは、地名の取得のためにOpenStreetMapまたはNominatimを呼び出していません。都道府県名と代表地点名はアプリとサーバーに固定した47地点の一覧から表示します。

## Discord

DiscordのWebhookおよびInteractions APIは、運用通知とチャート表示にのみ利用します。送信する内容は気圧・気温・判定・グラフで、アプリ利用者の個人情報は送信しません。利用時は[Discord Developer Terms of Service](https://support-dev.discord.com/hc/en-us/articles/8562894815383-Discord-Developer-Terms-of-Service)とレート制限に従います。

## Go依存パッケージ

バックエンドの追加依存パッケージは `golang.org/x/image` です。ライセンス表示は同パッケージの配布条件に従います。
