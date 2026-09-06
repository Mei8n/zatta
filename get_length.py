import os
from mutagen.oggvorbis import OggVorbis


def generate_sam_length():
    while True:
        domain_suffix = input("ドメイン名の ○○ 部分を入力してください (sound_○○): ").strip()
        if domain_suffix:
            break
        print("ドメイン名を入力してください")

    domain = f"sound_{domain_suffix}"

    # ドメイン入力後にファイル検索・秒数取得などの処理を開始する
    files = [f for f in os.listdir('.') if f.endswith('.ogg')]

    if not files:
        print("oggファイルが見つかりません")
        return

    output_file = "output_sam_length.txt"

    with open(output_file, "w", encoding="utf-8") as f:
        f.write("{\n")

        for i, filename in enumerate(files):
            try:
                audio = OggVorbis(filename)
                length = round(audio.info.length, 2)

                sound_id = os.path.splitext(filename)[0]

                f.write(f'  "{domain}:{sound_id}": {{\n')
                f.write(f'    "length": {length:.2f}\n')

                if i < len(files) - 1:
                    f.write('  },\n')
                else:
                    f.write('  }\n')

            except Exception as e:
                print(f"エラー: {filename} を読み込めませんでした {e}")

        f.write("}\n")

    print(f"完了しました。 {output_file} を確認してください。")


if __name__ == "__main__":
    generate_sam_length()
